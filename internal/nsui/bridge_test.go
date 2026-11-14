package nsui

import (
	"strings"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/internal/agent"
	"miya/internal/agent/cliupdate"
	"miya/internal/agent/login"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/memory/manager"
	"miya/internal/nswidgets"
	svcproto "miya/internal/service/protocol"
	"miya/internal/uiruntime"
)

// fakeHost holds controller state as the controller does: plain fields,
// changed on the UI thread, followed by uiruntime.Invalidate.
type fakeHost struct {
	threads      []model.Thread
	current      int
	state        appmodel.WindowState
	agents       []agent.Availability
	settings     []agent.Setting
	permission   string
	agentCalls   []string
	phone        svcproto.PhoneStatus
	phoneOK      bool
	phoneActions []string
	refreshes    int
	memory       *manager.Manager
	loginCall    func(login.Request) (login.State, error)
}

func newFakeHost(threads ...model.Thread) *fakeHost {
	h := &fakeHost{threads: threads, current: threads[0].ID}
	h.state.Ready = func() bool { return true }
	h.state.Cwd = "/workspace"
	h.state.SetPermissionPolicy = func(v string) { h.permission = v; uiruntime.Invalidate() }
	h.state.Threads = func() []model.Thread { return append([]model.Thread(nil), h.threads...) }
	return h
}

func (h *fakeHost) State() *appmodel.WindowState              { return &h.state }
func (h *fakeHost) EnsurePhoneHost()                          {}
func (h *fakeHost) RetryResponse(appmodel.RetryRequest) error { return nil }
func (h *fakeHost) RememberDraft(string)                      {}
func (h *fakeHost) SendDraft(bool) int                        { return 0 }
func (h *fakeHost) Stop()                                     {}
func (h *fakeHost) NewThread()                                {}

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
	if !app.Exists("id:thread-2") || !strings.Contains(strings.Join(app.Texts(), "\n"), "Beta") {
		t.Fatalf("row Beta missing; texts %q", app.Texts())
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
	if !app.Exists("id:thread-3") || !strings.Contains(strings.Join(app.Texts(), "\n"), "Gamma") {
		t.Fatalf("new row Gamma missing; texts %q", app.Texts())
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

func (h *fakeHost) AgentSettings() []agent.Setting { return h.settings }
func (h *fakeHost) Agents() []agent.Availability   { return h.agents }
func (h *fakeHost) SetAgentEnabled(id string, on bool) {
	h.agentCalls = append(h.agentCalls, id)
	for i := range h.agents {
		if h.agents[i].ID == id {
			h.agents[i].Enabled = on
		}
	}
	uiruntime.Invalidate()
}
func (h *fakeHost) RecheckAgents()                                               {}
func (h *fakeHost) EnsureAgentUpdates()                                          {}
func (h *fakeHost) AgentUpdates() map[string]cliupdate.Info                      { return nil }
func (h *fakeHost) AgentUpdateBusy() bool                                        { return false }
func (h *fakeHost) UpgradeAgent(string)                                          {}
func (h *fakeHost) SelectedPermissionPolicy() string                             { return h.permission }
func (h *fakeHost) RefreshAccount()                                              { h.refreshes++ }
func (h *fakeHost) AccountLoginTarget() func(login.Request) (login.State, error) { return h.loginCall }
func (h *fakeHost) RemoteHost() bool                                             { return false }
func (h *fakeHost) AskAccountReset()                                             {}
func (h *fakeHost) CancelAccountReset()                                          {}
func (h *fakeHost) ConfirmAccountReset()                                         {}
func (h *fakeHost) PhoneControls() (svcproto.PhoneStatus, func(string), bool) {
	return h.phone, func(op string) { h.phoneActions = append(h.phoneActions, op); uiruntime.Invalidate() }, h.phoneOK
}
func (h *fakeHost) Memories() *manager.Manager               { return h.memory }
func (h *fakeHost) AllThreads() []model.Thread               { return h.threads }
func (h *fakeHost) ThreadRef(t model.Thread) model.ThreadRef { return model.ThreadRef{} }
func (h *fakeHost) ThreadWorkspace(int) string               { return h.state.Cwd }
func (h *fakeHost) ChangeWorkspace(path string) error {
	h.state.Cwd = path
	uiruntime.Invalidate()
	return nil
}
