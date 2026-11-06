package controller

import (
	"fmt"

	"miya/internal/appmodel"
	"miya/internal/model"
	"miya/internal/msgfmt"
)

// retryResponse retries the latest failed response. A turn that failed before
// producing anything restarts at the last user message, just like editing it
// without changing its text. A turn that failed after it had already streamed
// replies or run tools keeps that output and asks the agent to continue, since
// rewinding would throw away work the user has already seen. Revalidate at the
// service to reject stale clicks from another window or phone, and leave the
// composer's unsent draft alone.
func (c *Controller) RetryResponse(req appmodel.RetryRequest) error {
	if c.Remote != nil {
		return c.Remote.Call("retry", req, nil)
	}
	t := c.ThreadWithID(req.ThreadID)
	if t.Busy || t.Loading || t.TaskPending {
		return fmt.Errorf("This conversation is running. Try again later.")
	}
	if len(t.Messages) == 0 || !msgfmt.RetryableError(t, t.Messages[len(t.Messages)-1]) || t.Messages[len(t.Messages)-1].ID != req.MessageID {
		return fmt.Errorf("This error is no longer current. Check the latest response.")
	}
	for i := len(t.Messages) - 2; i >= 0; i-- {
		m := t.Messages[i]
		if m.Role != "user" {
			continue
		}
		if msgfmt.FailedAfterOutput(t, i) {
			if !BackendOf(t).Ready(c) {
				return fmt.Errorf("Unable to retry. Check the connection or agent settings.")
			}
			if c.Submit(model.SendRequest{ThreadID: t.ID, Text: msgfmt.RetryContinuePrompt, KeepSessionSettings: true, MemoryControl: true}) == 0 {
				return fmt.Errorf("Unable to retry. Check the connection or agent settings.")
			}
			return nil
		}
		if !c.CanRewind(t, m) || !BackendOf(t).Ready(c) {
			return fmt.Errorf("Unable to retry. Check the connection or agent settings.")
		}
		text := msgfmt.MessagePrompt(m)
		if text == "" && len(msgfmt.MessageAttachments(m)) == 0 {
			return fmt.Errorf("Original message for retry not found")
		}
		c.Rewind(t.ID, m.ID, text)
		return nil
	}
	return fmt.Errorf("Original message for retry not found")
}
