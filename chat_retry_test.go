package main

import (
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

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
	for _, state := range []string{"busy", "loading", "pending", "readonly", "stale", "resume-error", "offline", "no-user", "empty-user"} {
		t.Run(state, func(t *testing.T) {
			app, _ := testApp(t)
			th := failedResponse()
			switch state {
			case "busy":
				th.Busy = true
			case "loading":
				th.Loading = true
			case "pending":
				th.TaskPending = true
			case "readonly":
				th.ReadOnly = true
			case "stale":
				th.Messages = append(th.Messages, Message{ID: 13, Role: "assistant", Content: "已恢复"})
			case "resume-error":
				th.Messages[2].ItemID = "resume-error"
			case "no-user":
				th.Messages = th.Messages[1:]
			case "empty-user":
				th.Agent = agentClaude
				app.c.claudeBinary = "/bin/true"
				th.Messages[0].Content = ""
				// Without earlier output, retry must resend the original message.
				th.Messages = append(th.Messages[:1], th.Messages[2])
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

func TestRetryMessageKeepsAttachmentsAndReferenceSnapshot(t *testing.T) {
	src := handoffSource()
	raw := referenceText(testReference(src), src.Title) + " 再审核一下"
	prompt, _, err := expandThreadRefs(raw, ThreadRef{}, func(ThreadRef) (Thread, []Message, error) {
		return src, src.Messages, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(t.TempDir(), "screen shot.png"), filepath.Join(t.TempDir(), "notes.txt")}
	m := Message{Content: prompt + "\n📎 notes.txt", Attachments: paths, Images: []string{(&url.URL{Scheme: "file", Path: paths[0]}).String()}}
	if got := messagePrompt(m); got != prompt {
		t.Fatal("retry changed the original reference or its context snapshot")
	}
	if got := messageAttachments(m); !reflect.DeepEqual(got, paths) {
		t.Fatalf("retry lost or duplicated attachment paths: %q", got)
	}
	// Older messages only recorded the image URL, including escaped spaces.
	m = Message{Images: m.Images}
	if got := messageAttachments(m); !reflect.DeepEqual(got, paths[:1]) || messagePrompt(m) != "" {
		t.Fatalf("retry lost an older image-only prompt: %q", got)
	}
}

func TestRetryAfterStreamedOutputContinuesInsteadOfRewinding(t *testing.T) {
	app, _ := testApp(t)
	th := failedResponse()
	th.Agent = agentClaude
	app.c.claudeBinary = "/bin/true"
	th.Messages = append(th.Messages[:2], Message{ID: 14, Role: "assistant", Content: "已经写好的长回复"}, th.Messages[2])
	app.c.setThreads([]Thread{th})
	if err := app.c.retryResponse(retryRequest{ThreadID: 1, MessageID: 12}); err != nil {
		t.Fatal(err)
	}
	got := app.c.threadWithID(1).Messages
	if len(got) < 4 || got[1].ID != 11 || got[2].ID != 14 || got[3].ID != 12 {
		t.Fatalf("retry discarded output streamed before the error: %+v", got)
	}
	var continued bool
	for _, m := range got[4:] {
		if m.Role == "user" && strings.Contains(m.Content, retryContinuePrompt) {
			continued = true
		}
	}
	if !continued {
		t.Fatalf("retry did not ask the agent to continue: %+v", got)
	}
}

func TestFailedAfterOutput(t *testing.T) {
	th := failedResponse()
	if !failedAfterOutput(th, 0) {
		t.Fatal("tool output before the error was not detected")
	}
	th.Messages = append(th.Messages[:1], th.Messages[2])
	if failedAfterOutput(th, 0) {
		t.Fatal("an error alone counted as output")
	}
}
