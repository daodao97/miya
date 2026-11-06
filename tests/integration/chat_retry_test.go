package integration

import (
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"miya/internal/appmodel"
	"miya/internal/controller"
	"miya/internal/model"
	"miya/internal/msgfmt"
)

func failedResponse() model.Thread {
	return model.Thread{ID: 1, ServerID: "s1", Resumed: true, Loaded: true, HistoryLoaded: true, Messages: []model.Message{
		{ID: 10, Role: "user", Content: "安装应用"},
		{ID: 11, Role: "tool", Content: "构建完成", Status: "completed"},
		{ID: 12, Role: "error", Content: "API Error: Connection dropped (ECONNRESET)."},
	}}
}

func TestErrorCardOffersRetryWithoutEditingDraft(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetThreads([]model.Thread{failedResponse()})
	c.SetActive(1)
	c.SetDraft("尚未发送的下一条消息")
	c.SetAttachments([]string{"/tmp/attachment.txt"})
	var got appmodel.RetryRequest
	app.OnRetry = func(req appmodel.RetryRequest) { got = req }
	tt.Frame()
	if err := tt.Click("Retry response"); err != nil {
		t.Fatal(err)
	}
	if got != (appmodel.RetryRequest{ThreadID: 1, MessageID: 12}) {
		t.Fatalf("retried wrong response: %+v", got)
	}
	if c.Draft() != "尚未发送的下一条消息" || len(c.Attachments()) != 1 {
		t.Fatal("retry changed the unsent draft")
	}
	got = appmodel.RetryRequest{}
	c.Update(1, func(t *model.Thread) { t.Busy = true })
	tt.Frame()
	_ = tt.Click("Retry response")
	if got.ThreadID != 0 {
		t.Fatal("retry was allowed while the turn was running")
	}
	c.Update(1, func(t *model.Thread) {
		t.Busy = false
		t.Messages = append(t.Messages, model.Message{ID: 13, Role: "user", Content: "新的请求"})
	})
	tt.Frame()
	if _, ok := tt.Find("Retry response"); ok {
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
				th.Messages = append(th.Messages, model.Message{ID: 13, Role: "assistant", Content: "已恢复"})
			case "resume-error":
				th.Messages[2].ItemID = "resume-error"
			case "no-user":
				th.Messages = th.Messages[1:]
			case "empty-user":
				th.Agent = appmodel.AgentClaude
				app.C.(*controller.Controller).ClaudeBinary = "/bin/true"
				th.Messages[0].Content = ""
				// Without earlier output, retry must resend the original message.
				th.Messages = append(th.Messages[:1], th.Messages[2])
			}
			app.C.(*controller.Controller).SetThreads([]model.Thread{th})
			if err := app.C.(*controller.Controller).RetryResponse(appmodel.RetryRequest{ThreadID: 1, MessageID: 12}); err == nil {
				t.Fatal("unavailable response was retried")
			}
			if len(app.C.(*controller.Controller).ThreadWithID(1).Messages) != len(th.Messages) || len(app.C.(*controller.Controller).ThreadWithID(1).Queue) != 0 {
				t.Fatal("rejected retry changed the conversation")
			}
		})
	}
}

func TestRetryMessageKeepsAttachmentsAndReferenceSnapshot(t *testing.T) {
	src := handoffSource()
	raw := model.ReferenceText(testReference(src), src.Title) + " 再审核一下"
	prompt, _, err := controller.ExpandThreadRefs(raw, model.ThreadRef{}, func(model.ThreadRef) (model.Thread, []model.Message, error) {
		return src, src.Messages, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(t.TempDir(), "screen shot.png"), filepath.Join(t.TempDir(), "notes.txt")}
	m := model.Message{Content: prompt + "\n📎 notes.txt", Attachments: paths, Images: []string{(&url.URL{Scheme: "file", Path: paths[0]}).String()}}
	if got := msgfmt.MessagePrompt(m); got != prompt {
		t.Fatal("retry changed the original reference or its context snapshot")
	}
	if got := msgfmt.MessageAttachments(m); !reflect.DeepEqual(got, paths) {
		t.Fatalf("retry lost or duplicated attachment paths: %q", got)
	}
	// Older messages only recorded the image URL, including escaped spaces.
	m = model.Message{Images: m.Images}
	if got := msgfmt.MessageAttachments(m); !reflect.DeepEqual(got, paths[:1]) || msgfmt.MessagePrompt(m) != "" {
		t.Fatalf("retry lost an older image-only prompt: %q", got)
	}
}

func TestRetryAfterStreamedOutputContinuesInsteadOfRewinding(t *testing.T) {
	app, _ := testApp(t)
	th := failedResponse()
	th.Agent = appmodel.AgentClaude
	app.C.(*controller.Controller).ClaudeBinary = "/bin/true"
	th.Messages = append(th.Messages[:2], model.Message{ID: 14, Role: "assistant", Content: "已经写好的长回复"}, th.Messages[2])
	app.C.(*controller.Controller).SetThreads([]model.Thread{th})
	if err := app.C.(*controller.Controller).RetryResponse(appmodel.RetryRequest{ThreadID: 1, MessageID: 12}); err != nil {
		t.Fatal(err)
	}
	got := app.C.(*controller.Controller).ThreadWithID(1).Messages
	if len(got) < 4 || got[1].ID != 11 || got[2].ID != 14 || got[3].ID != 12 {
		t.Fatalf("retry discarded output streamed before the error: %+v", got)
	}
	var continued bool
	for _, m := range got[4:] {
		if m.Role == "user" && strings.Contains(m.Content, msgfmt.RetryContinuePrompt) {
			continued = true
		}
	}
	if !continued {
		t.Fatalf("retry did not ask the agent to continue: %+v", got)
	}
}

func TestFailedAfterOutput(t *testing.T) {
	th := failedResponse()
	if !msgfmt.FailedAfterOutput(th, 0) {
		t.Fatal("tool output before the error was not detected")
	}
	th.Messages = append(th.Messages[:1], th.Messages[2])
	if msgfmt.FailedAfterOutput(th, 0) {
		t.Fatal("an error alone counted as output")
	}
}
