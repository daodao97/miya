package main

import "fmt"

type retryRequest struct {
	ThreadID, MessageID int
}

// Only the latest failed response can be continued. Old error cards must not
// start another turn after the conversation has already moved on.
func retryableError(t Thread, m Message) bool {
	if t.ID == 0 || t.ReadOnly || m.Role != "error" || m.ItemID == "resume-error" || len(t.Messages) == 0 {
		return false
	}
	if t.Messages[len(t.Messages)-1].ID != m.ID {
		return false
	}
	for _, previous := range t.Messages[:len(t.Messages)-1] {
		if previous.Role == "user" {
			return true
		}
	}
	return false
}

// retryResponse uses the existing session, preserving completed tool work and
// the composer's unsent draft. Revalidate at the service to reject stale clicks
// from another window or phone.
func (c *chatController) retryResponse(req retryRequest) error {
	if c.remote != nil {
		return c.remote.call("retry", req, nil)
	}
	t := c.threadWithID(req.ThreadID)
	if t.Busy || t.Loading {
		return fmt.Errorf("对话正在运行，请稍后重试")
	}
	if len(t.Messages) == 0 || !retryableError(t, t.Messages[len(t.Messages)-1]) || t.Messages[len(t.Messages)-1].ID != req.MessageID {
		return fmt.Errorf("此错误已过期，请查看最新回复")
	}
	if c.submit(sendRequest{ThreadID: t.ID, Text: "继续"}) == 0 {
		return fmt.Errorf("暂时无法重试，请检查连接或 Agent 设置")
	}
	return nil
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
