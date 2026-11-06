package main

import "fmt"

type retryRequest struct {
	ThreadID, MessageID int
}

// retryResponse retries the latest failed response. A turn that failed before
// producing anything restarts at the last user message, just like editing it
// without changing its text. A turn that failed after it had already streamed
// replies or run tools keeps that output and asks the agent to continue, since
// rewinding would throw away work the user has already seen. Revalidate at the
// service to reject stale clicks from another window or phone, and leave the
// composer's unsent draft alone.
func (c *chatController) retryResponse(req retryRequest) error {
	if c.remote != nil {
		return c.remote.Call("retry", req, nil)
	}
	t := c.threadWithID(req.ThreadID)
	if t.Busy || t.Loading || t.TaskPending {
		return fmt.Errorf("This conversation is running. Try again later.")
	}
	if len(t.Messages) == 0 || !retryableError(t, t.Messages[len(t.Messages)-1]) || t.Messages[len(t.Messages)-1].ID != req.MessageID {
		return fmt.Errorf("This error is no longer current. Check the latest response.")
	}
	for i := len(t.Messages) - 2; i >= 0; i-- {
		m := t.Messages[i]
		if m.Role != "user" {
			continue
		}
		if failedAfterOutput(t, i) {
			if !backendOf(t).Ready(c) {
				return fmt.Errorf("Unable to retry. Check the connection or agent settings.")
			}
			if c.submit(SendRequest{ThreadID: t.ID, Text: retryContinuePrompt, KeepSessionSettings: true, MemoryControl: true}) == 0 {
				return fmt.Errorf("Unable to retry. Check the connection or agent settings.")
			}
			return nil
		}
		if !c.canRewind(t, m) || !backendOf(t).Ready(c) {
			return fmt.Errorf("Unable to retry. Check the connection or agent settings.")
		}
		text := messagePrompt(m)
		if text == "" && len(messageAttachments(m)) == 0 {
			return fmt.Errorf("Original message for retry not found")
		}
		c.rewind(t.ID, m.ID, text)
		return nil
	}
	return fmt.Errorf("Original message for retry not found")
}

func (app *chatApp) retryResponse(m Message) {
	req := retryRequest{ThreadID: app.c.current().ID, MessageID: m.ID}
	if app.onRetry != nil {
		app.onRetry(req)
		return
	}
	if err := app.c.retryResponse(req); err != nil {
		app.notice = err.Error()
	}
}
