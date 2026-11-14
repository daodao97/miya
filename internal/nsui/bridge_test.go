package nsui

import (
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/nswidgets"
	"miya/internal/uiruntime"
)

// fakeHost holds controller state as the controller does: plain fields,
// changed on the UI thread, followed by uiruntime.Invalidate.
type fakeHost struct {
	threads []model.Thread
	current int
	state   appmodel.WindowState
}

func newFakeHost(threads ...model.Thread) *fakeHost {
	h := &fakeHost{threads: threads, current: threads[0].ID}
	h.state.Threads = func() []model.Thread { return append([]model.Thread(nil), h.threads...) }
	return h
}

func (h *fakeHost) State() *appmodel.WindowState              { return &h.state }
func (h *fakeHost) EnsurePhoneHost()                          {}
func (h *fakeHost) RetryResponse(appmodel.RetryRequest) error { return nil }
func (h *fakeHost) RememberDraft(string)                      {}
func (h *fakeHost) SendDraft(bool) int                        { return 0 }
func (h *fakeHost) Stop()                                     {}

func (h *fakeHost) Current() model.Thread {
	for _, t := range h.threads {
		if t.ID == h.current {
			return t
		}
	}
	return model.Thread{}
}

func (h *fakeHost) SelectThread(t model.Thread) {
	h.current = t.ID
	uiruntime.Invalidate()
}

func startBridged(t *testing.T, h Host) *nsgotest.App {
	t.Helper()
	t.Cleanup(func() {
		uiruntime.SetAppWindow(nil)
		current = nil
	})
	return nsgotest.Start(t, func(cx *core.Scope) core.Node {
		installBridge(cx)
		cx.SetTheme(nswidgets.Theme())
		return root(cx, h)
	}, nsgotest.Size(900, 600))
}

func TestBridgeShowsControllerState(t *testing.T) {
	h := newFakeHost(model.Thread{ID: 1, Title: "Alpha"}, model.Thread{ID: 2, Title: "Beta"})
	app := startBridged(t, h)
	if got := app.Find("id:current-title").Text; got != "Alpha" {
		t.Fatalf("title = %q, want Alpha", got)
	}
	if got := app.Find("id:thread-2").Text; got != "Beta" {
		t.Fatalf("row = %q, want Beta", got)
	}
}

// A press reaches the controller, and its Invalidate updates the view.
func TestBridgeSelectsThroughController(t *testing.T) {
	h := newFakeHost(model.Thread{ID: 1, Title: "Alpha"}, model.Thread{ID: 2, Title: "Beta"})
	app := startBridged(t, h)
	app.Press("id:thread-2")
	app.Settle()
	if h.current != 2 {
		t.Fatalf("controller current = %d, want 2", h.current)
	}
	if got := app.Find("id:current-title").Text; got != "Beta" {
		t.Fatalf("title after press = %q, want Beta", got)
	}
}

// Changes the controller makes on its own, as a reply arriving, show after
// an Invalidate from any goroutine; many Invalidates make one update.
func TestBridgeFollowsInvalidate(t *testing.T) {
	h := newFakeHost(model.Thread{ID: 1, Title: "Alpha"})
	app := startBridged(t, h)
	app.Post(func() {
		h.threads[0].Title = "Alpha renamed"
		h.threads = append(h.threads, model.Thread{ID: 3, Title: "Gamma"})
	})
	before := current.rev.GetUntracked()
	for range 5 {
		uiruntime.Invalidate()
	}
	app.Settle()
	if got := app.Find("id:current-title").Text; got != "Alpha renamed" {
		t.Fatalf("title = %q, want Alpha renamed", got)
	}
	if got := app.Find("id:thread-3").Text; got != "Gamma" {
		t.Fatalf("new row = %q, want Gamma", got)
	}
	if bumps := current.rev.GetUntracked() - before; bumps < 1 || bumps > 2 {
		t.Fatalf("5 invalidates made %d revisions, want them coalesced", bumps)
	}
}

// Update runs work on the engine goroutine, then shows its result.
func TestBridgeUpdateRunsOnEngine(t *testing.T) {
	h := newFakeHost(model.Thread{ID: 1, Title: "Alpha"}, model.Thread{ID: 2, Title: "Beta"})
	app := startBridged(t, h)
	uiruntime.RunOnMain(func() { h.current = 2 })
	app.Settle()
	if got := app.Find("id:current-title").Text; got != "Beta" {
		t.Fatalf("title = %q, want Beta", got)
	}
}
