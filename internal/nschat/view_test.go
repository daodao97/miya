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
		t.Fatal("latest error after a user prompt must offer retry")
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

// The composer edits the controller's draft, sends it, and stops a turn.
func TestComposerSendsAndStops(t *testing.T) {
	var sent []string
	stopped := 0
	c := &conversation{thread: model.Thread{ID: 1, HistoryLoaded: true}}
	var draft *core.Signal[string]
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		cx.SetTheme(nswidgets.Theme())
		c.rev = core.NewSignal(cx, 0)
		draft = core.NewSignal(cx, "")
		return View{Thread: c.get, Draft: draft.Get, OnDraftChange: draft.Set,
			OnSend: func() { sent = append(sent, draft.GetUntracked()); draft.Set("") },
			OnStop: func() { stopped++ }}
	}, nsgotest.Size(900, 800))
	app.Type("label:Message", "Run the tests")
	app.Press("label:Send")
	app.Settle()
	if len(sent) != 1 || sent[0] != "Run the tests" {
		t.Fatalf("sent = %q", sent)
	}
	c.change(app, func(t *model.Thread) { t.Busy = true })
	app.Press("label:Stop")
	app.Settle()
	if stopped != 1 {
		t.Fatalf("stopped %d times, want 1", stopped)
	}
}

func TestPromptShowsImagesAndFiles(t *testing.T) {
	app, _ := start(t, model.Thread{ID: 1, HistoryLoaded: true, Messages: []model.Message{
		{ID: 1, Role: "user", Content: "Look at this", Images: []string{"/tmp/shot.png"}, Attachments: []string{"/work/notes.md"}, TurnID: "t1"},
	}}, nil)
	if !app.Exists("id:image-1-0") {
		t.Error("image thumbnail missing")
	}
	if text := joined(app); !strings.Contains(text, "notes.md") {
		t.Errorf("attachment chip missing from %q", text)
	}
	if got := imageURI("/tmp/a b.png"); got != "file:///tmp/a%20b.png" {
		t.Errorf("imageURI = %q", got)
	}
	if got := imageURI("data:image/png;base64,AA=="); got != "data:image/png;base64,AA==" {
		t.Errorf("imageURI(data) = %q", got)
	}
}

// A finished response offers its actions; a streaming one does not yet.
func TestResponseActionsAfterTurn(t *testing.T) {
	handoffs := 0
	c := &conversation{thread: model.Thread{ID: 1, Busy: true, HistoryLoaded: true, Messages: []model.Message{
		{ID: 1, Role: "user", Content: "Hi", TurnID: "t1"},
		{ID: 2, Role: "assistant", Content: "Working on it", Status: "inProgress", TurnID: "t1"},
	}}}
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		cx.SetTheme(nswidgets.Theme())
		c.rev = core.NewSignal(cx, 0)
		return View{Thread: c.get, OnHandoff: func() { handoffs++ }}
	}, nsgotest.Size(900, 800))
	if app.Exists("id:handoff") {
		t.Fatal("actions shown while the reply streams")
	}
	c.change(app, func(t *model.Thread) { t.Busy = false; t.Messages[1].Status = "completed" })
	if !app.Exists("id:handoff") {
		t.Fatalf("actions missing after the turn; texts %q", joined(app))
	}
	app.Press("id:handoff")
	app.Settle()
	if handoffs != 1 {
		t.Fatalf("handoffs = %d", handoffs)
	}
}

// A sub-agent row updates in place as the sub-agent works.
func TestSubAgentsUpdateInPlace(t *testing.T) {
	var agents *core.Signal[[]model.SubAgent]
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		cx.SetTheme(nswidgets.Theme())
		agents = core.NewSignalFunc(cx, []model.SubAgent{{ID: "a", Name: "Write tests", Status: "running", Detail: "reading files"}},
			func(x, y []model.SubAgent) bool { return false })
		return subAgentList(cx, agents.Get)
	}, nsgotest.Size(400, 300))
	if !strings.Contains(joined(app), "Write tests") || !strings.Contains(joined(app), "reading files") {
		t.Fatalf("sub-agent missing: %q", joined(app))
	}
	before := app.Find("id:subagent-a").ID
	app.Post(func() {
		agents.Set([]model.SubAgent{{ID: "a", Name: "Write tests", Status: "completed", Detail: "all green"}, {ID: "b", Name: "Review", Status: "running"}})
	})
	app.Settle()
	if text := joined(app); !strings.Contains(text, "all green") || !strings.Contains(text, "Review") || !strings.Contains(text, "✓") {
		t.Fatalf("sub-agents did not update: %q", text)
	}
	if app.Find("id:subagent-a").ID != before {
		t.Error("sub-agent row rebuilt on update")
	}
}

func TestSubAgentLine(t *testing.T) {
	if got := subAgentLine(model.SubAgent{Status: "running", Steps: 3, Detail: "editing"}); !strings.Contains(got, "3") || !strings.HasSuffix(got, "editing") {
		t.Errorf("line = %q", got)
	}
	if marker("completed") != "✓" || marker("weird") != "○" {
		t.Error("markers")
	}
}

// A phone resolves desktop images asynchronously without changing the source
// carried by synchronized history or rebuilding the image's native view.
func TestPhoneImageResolverUpdatesTheExistingImage(t *testing.T) {
	var path *core.Signal[string]
	th := model.Thread{ID: 7, Messages: []model.Message{{ID: 1, Role: "user", Content: "Photo", Images: []string{"file:///desktop/photo.jpg"}}}}
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		cx.SetTheme(nswidgets.Theme())
		path = core.NewSignal(cx, "")
		return View{Thread: func() model.Thread { return th }, ImageSource: func(_ *core.Scope, src string) func() string {
			if src != "file:///desktop/photo.jpg" {
				t.Errorf("unexpected source %q", src)
			}
			return path.Get
		}}
	}, nsgotest.Size(390, 844), nsgotest.Environment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone,
		SafeArea: core.Edges{Top: 47, Bottom: 34}}))
	before := app.Find("id:image-1-0")
	if before.Image.Source.URI != "" {
		t.Fatal("phone opened desktop filesystem path")
	}
	id := before.ID
	app.Post(func() { path.Set("/phone/cache/photo.jpg") })
	app.Settle()
	after := app.Find("id:image-1-0")
	if after.ID != id || after.Image.Source.URI != "file:///phone/cache/photo.jpg" {
		t.Fatalf("resolved image: id %d source %q", after.ID, after.Image.Source.URI)
	}
	if th.Messages[0].Images[0] != "file:///desktop/photo.jpg" {
		t.Fatal("resolver changed synchronized history")
	}
	app.Press("id:preview-image-1-0")
	full := app.Find("id:message-image-full")
	if full.Image.Source.URI != after.Image.Source.URI || full.Image.ResizeMode != core.Contain {
		t.Fatalf("phone preview did not use its cached image: %+v", full.Image)
	}
	app.Press("id:message-image-close")
	if app.Exists("id:message-image-preview") {
		t.Fatal("image preview did not close")
	}
}

func TestTranscriptImagePreviewUsesFullImageAndClosesOnNavigation(t *testing.T) {
	app, c := start(t, model.Thread{ID: 1, Messages: []model.Message{
		{ID: 1, Role: "user", Content: "Screenshot", Images: []string{"/tmp/shot image.png"}},
		{ID: 2, Role: "assistant", Content: `<img src="file:///tmp/report.png" alt="Rendered report" width="80" height="80">`},
	}}, nil)
	app.Press("id:preview-image-1-0")
	img := app.Find("id:message-image-full")
	if img.Image.Source.URI != "file:///tmp/shot%20image.png" || img.Image.ResizeMode != core.Contain ||
		img.Bounds().Size.Width <= 80 || img.Bounds().Size.Height <= 80 {
		t.Fatalf("unexpected preview source or size: %q %v", img.Image.Source.URI, img.Bounds())
	}
	app.Key("escape")
	if app.Exists("id:message-image-preview") {
		t.Fatal("Escape did not close the image preview")
	}
	app.Press("label:Rendered report")
	if got := app.Find("id:message-image-full").Image.Source.URI; got != "file:///tmp/report.png" {
		t.Fatalf("Markdown image opened %q", got)
	}
	c.change(app, func(th *model.Thread) { *th = model.Thread{ID: 2} })
	if app.Exists("id:message-image-preview") {
		t.Fatal("preview stayed open after leaving the conversation")
	}
}
