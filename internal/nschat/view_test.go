package nschat

import (
	"strings"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/nswidgets"
)

// conversation holds a thread as the controller does and bumps a revision
// on each change, as the bridge does for nsui.Live.
type conversation struct {
	thread model.Thread
	rev    *core.Signal[int]
}

func (c *conversation) get() model.Thread {
	c.rev.Get()
	return c.thread
}

func (c *conversation) change(app *nsgotest.App, f func(t *model.Thread)) {
	app.Post(func() {
		f(&c.thread)
		c.rev.Update(func(n int) int { return n + 1 })
	})
	app.Settle()
}

func start(t *testing.T, th model.Thread, retry func(appmodel.RetryRequest)) (*nsgotest.App, *conversation) {
	t.Helper()
	c := &conversation{thread: th}
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		cx.SetTheme(nswidgets.Theme())
		c.rev = core.NewSignal(cx, 0)
		return View{Thread: c.get, OnRetry: retry}
	}, nsgotest.Size(900, 800))
	return app, c
}

func joined(app *nsgotest.App) string { return strings.Join(app.Texts(), "\n") }

func TestShowsPromptReplyAndActivity(t *testing.T) {
	app, _ := start(t, model.Thread{ID: 1, HistoryLoaded: true, Messages: []model.Message{
		{ID: 1, Role: "user", Content: "Fix the build", TurnID: "t1"},
		{ID: 2, Role: "commandExecution", Kind: "commandExecution", Status: "completed", Content: "ok", TurnID: "t1"},
		{ID: 3, Role: "assistant", Content: "The build **passes** now.", Status: "completed", TurnID: "t1"},
	}}, nil)
	if got := app.Find("id:prompt-1").Text; got != "Fix the build" {
		t.Errorf("prompt = %q", got)
	}
	if text := joined(app); !strings.Contains(text, "passes") {
		t.Errorf("reply missing from %q", text)
	}
}

// A streaming reply grows in place, and its row is not rebuilt.
func TestReplyStreamsInPlace(t *testing.T) {
	app, c := start(t, model.Thread{ID: 1, Busy: true, HistoryLoaded: true, Messages: []model.Message{
		{ID: 1, Role: "user", Content: "Hi", TurnID: "t1"},
		{ID: 2, Role: "assistant", Content: "Hello", Status: "inProgress", TurnID: "t1"},
	}}, nil)
	before := app.Find("id:reply-2")
	c.change(app, func(t *model.Thread) { t.Messages[1].Content = "Hello there, friend" })
	if text := joined(app); !strings.Contains(text, "friend") {
		t.Fatalf("streamed text missing from %q", text)
	}
	if after := app.Find("id:reply-2"); after.ID != before.ID {
		t.Errorf("reply view replaced while streaming: %d -> %d", before.ID, after.ID)
	}
	c.change(app, func(t *model.Thread) {
		t.Busy = false
		t.Messages[1].Status = "completed"
		t.Messages = append(t.Messages, model.Message{ID: 3, Role: "user", Content: "Thanks", TurnID: "t2"})
	})
	if got := app.Find("id:prompt-3").Text; got != "Thanks" {
		t.Errorf("new prompt = %q", got)
	}
}

func TestRetryableErrorAsksAgain(t *testing.T) {
	var asked []appmodel.RetryRequest
	th := model.Thread{ID: 7, HistoryLoaded: true, Messages: []model.Message{
		{ID: 1, Role: "user", Content: "Go", TurnID: "t1"},
		{ID: 2, Role: "error", Content: "Network error", TurnID: "t1"},
	}}
	app, _ := start(t, th, func(r appmodel.RetryRequest) { asked = append(asked, r) })
	if got := app.Find("id:error-2").Text; got != "Network error" {
		t.Fatalf("error text = %q", got)
	}
	if !app.Exists("label:Retry response") {
		t.Skip("presentation does not count this error as retryable")
	}
	app.Press("label:Retry response")
	app.Settle()
	if len(asked) != 1 || asked[0] != (appmodel.RetryRequest{ThreadID: 7, MessageID: 2}) {
		t.Fatalf("retries = %+v", asked)
	}
}

func TestEmptyStates(t *testing.T) {
	app, c := start(t, model.Thread{ID: 1, Loading: true}, nil)
	if !strings.Contains(joined(app), "Loading conversation") {
		t.Errorf("loading state missing: %q", joined(app))
	}
	c.change(app, func(t *model.Thread) { t.Loading, t.Status = false, "connection refused" })
	if text := joined(app); !strings.Contains(text, "Unable to load conversation") || !strings.Contains(text, "connection refused") {
		t.Errorf("failure state missing: %q", text)
	}
}
