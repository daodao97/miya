package main

import "testing"

func failedResponse() Thread {
	return Thread{ID: 1, ServerID: "s1", Resumed: true, Loaded: true, HistoryLoaded: true, Messages: []Message{
		{ID: 10, Role: "user", Content: "安装应用"},
		{ID: 11, Role: "tool", Content: "构建完成", Status: "completed"},
		{ID: 12, Role: "error", Content: "API Error: Connection dropped (ECONNRESET)."},
	}}
}

func TestErrorCardOffersRetryWithoutEditingDraft(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	c.setThreads([]Thread{failedResponse()})
	c.setActive(1)
	c.setDraft("尚未发送的下一条消息")
	c.setAttachments([]string{"/tmp/attachment.txt"})
	var got retryRequest
	app.onRetry = func(req retryRequest) { got = req }
	tt.Frame()
	if err := tt.Click("重试回复"); err != nil {
		t.Fatal(err)
	}
	if got != (retryRequest{ThreadID: 1, MessageID: 12}) {
		t.Fatalf("retried wrong response: %+v", got)
	}
	if c.draft() != "尚未发送的下一条消息" || len(c.attachments()) != 1 {
		t.Fatal("retry changed the unsent draft")
	}
	got = retryRequest{}
	c.update(1, func(t *Thread) { t.Busy = true })
	tt.Frame()
	_ = tt.Click("重试回复")
	if got.ThreadID != 0 {
		t.Fatal("retry was allowed while the turn was running")
	}
	c.update(1, func(t *Thread) {
		t.Busy = false
		t.Messages = append(t.Messages, Message{ID: 13, Role: "user", Content: "新的请求"})
	})
	tt.Frame()
	if _, ok := tt.Find("重试回复"); ok {
		t.Fatal("an old error still offers retry after a new request")
	}
}

func TestRetryRejectsUnavailableOrStaleResponse(t *testing.T) {
	for _, state := range []string{"busy", "loading", "readonly", "stale", "resume-error", "offline"} {
		t.Run(state, func(t *testing.T) {
			app, _ := testApp(t)
			th := failedResponse()
			switch state {
			case "busy":
				th.Busy = true
			case "loading":
				th.Loading = true
			case "readonly":
				th.ReadOnly = true
			case "stale":
				th.Messages = append(th.Messages, Message{ID: 13, Role: "assistant", Content: "已恢复"})
			case "resume-error":
				th.Messages[2].ItemID = "resume-error"
			}
			app.c.setThreads([]Thread{th})
			if err := app.c.retryResponse(retryRequest{ThreadID: 1, MessageID: 12}); err == nil {
				t.Fatal("unavailable response was retried")
			}
			if len(app.c.threadWithID(1).Messages) != len(th.Messages) || len(app.c.threadWithID(1).Queue) != 0 {
				t.Fatal("rejected retry changed the conversation")
			}
		})
	}
}
