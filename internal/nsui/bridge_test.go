package nsui

import (
	"strings"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"miya/internal/agent"
	agentmeta "miya/internal/agent/catalog"
	"miya/internal/agent/cliupdate"
	"miya/internal/agent/login"
	"miya/internal/appmodel"
	"miya/internal/automation"
	"miya/internal/chat/model"
	"miya/internal/interaction"
	"miya/internal/memory/manager"
	"miya/internal/nswidgets"
	svcproto "miya/internal/service/protocol"
	"miya/internal/uiruntime"
)

// fakeHost holds controller state as the controller does: plain fields,
// changed on the UI thread, followed by uiruntime.Invalidate.
type fakeHost struct {
	collaborationCall func(appmodel.CollaborationRequest) (any, error)
	draft             string
	attachments       []string
	controls          fakeControls
	sent              []bool
	rewound           struct {
		threadID, messageID int
		text                string
	}
	prefetched    []model.ThreadRef
	refs          map[int]model.ThreadRef
	canRewind     bool
	queuedActions []string
	collab        *model.CollaborationStatus

	answered     []interaction.Response
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
	h.state.Draft = func() string { return h.draft }
	h.state.Attachments = func() []string { return append([]string(nil), h.attachments...) }
	h.state.Active = func() int { return h.current }
	h.state.SetAgent = func(v string) { h.controls.id = v; uiruntime.Invalidate() }
	h.state.ReferenceSource = "local"
	h.state.Ready = func() bool { return true }
	h.state.Cwd = "/workspace"
	h.state.SetPermissionPolicy = func(v string) { h.permission = v; uiruntime.Invalidate() }
	h.state.Threads = func() []model.Thread { return append([]model.Thread(nil), h.threads...) }
	return h
}

func (h *fakeHost) State() *appmodel.WindowState              { return &h.state }
func (h *fakeHost) EnsurePhoneHost()                          {}
func (h *fakeHost) RetryResponse(appmodel.RetryRequest) error { return nil }
func (h *fakeHost) RememberDraft(v string)                    { h.draft = v; uiruntime.Invalidate() }
func (h *fakeHost) SendDraft(steer bool) int {
	h.sent = append(h.sent, steer)
	h.draft = ""
	h.attachments = nil
	uiruntime.Invalidate()
	return h.current
}
func (h *fakeHost) Stop() {}
func (h *fakeHost) NewThread() {
	h.current = 0
	h.draft = ""
	h.attachments = nil
	uiruntime.Invalidate()
}
func (h *fakeHost) DescribeApproval(a interaction.Request) appmodel.ApprovalView {
	return appmodel.ApprovalView{Title: "Run a command", Icon: "terminal-2", Code: a.Command}
}
func (h *fakeHost) Answer(a interaction.Request, r interaction.Response) {
	h.answered = append(h.answered, r)
}
func (h *fakeHost) RespondMCPAlways(interaction.Request) error { return nil }

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

// The controller reads the system's idle time through uiruntime once the
// platform answers, and the window's focus without touching NSGo's graph.
func TestPlatformServicesReachController(t *testing.T) {
	t.Cleanup(func() { uiruntime.IdleTime = nil })
	uiruntime.IdleTime = nil
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		sampleIdle(cx)
		return core.View{}
	}, nsgotest.Size(100, 100), nsgotest.Environment(core.Environment{OS: core.OSMacOS}))
	app.SystemIdle = 90 * time.Second
	app.Advance(idleEvery)
	app.Settle()
	if uiruntime.IdleTime == nil {
		t.Fatal("idle time not installed")
	}
	if d := uiruntime.IdleTime(); d < 90*time.Second {
		t.Fatalf("idle = %v, want at least 90s", d)
	}
}

// Where the platform has no idle clock the user counts as present.
func TestIdleTimeUnsupportedStaysUnset(t *testing.T) {
	t.Cleanup(func() { uiruntime.IdleTime = nil })
	uiruntime.IdleTime = nil
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		sampleIdle(cx)
		return core.View{}
	}, nsgotest.Size(100, 100), nsgotest.Environment(core.Environment{OS: core.OSLinux}))
	app.Advance(idleEvery)
	app.Settle()
	if uiruntime.IdleTime != nil {
		t.Fatal("idle time installed on a platform without one")
	}
	if uiruntime.Idle() != 0 {
		t.Fatal("Idle without a platform clock is not zero")
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
func (h *fakeHost) ThreadRef(t model.Thread) model.ThreadRef { return h.refs[t.ID] }
func (h *fakeHost) ThreadWorkspace(int) string               { return h.state.Cwd }
func (h *fakeHost) ChangeWorkspace(path string) error {
	h.state.Cwd = path
	uiruntime.Invalidate()
	return nil
}
func (h *fakeHost) AutomationOp(string, automation.Request) (any, error) { return nil, nil }
func (h *fakeHost) EnabledAgents() []string                              { return model.Backends }
func (h *fakeHost) ThreadWithID(id int) model.Thread {
	for _, t := range h.threads {
		if t.ID == id {
			return t
		}
	}
	return model.Thread{}
}

// The sidebar opens automations; choosing a conversation comes back.
func TestShellOpensAutomationsAndReturns(t *testing.T) {
	h := newFakeHost(model.Thread{ID: 1, Title: "Alpha"}, model.Thread{ID: 2, Title: "Beta"})
	app := startBridged(t, h)
	app.Press("id:open-automations")
	app.Settle()
	if !app.Exists("id:automations") {
		t.Fatal("automations page not shown")
	}
	if got := app.Find("id:current-title").Text; got != "Automations" {
		t.Fatalf("title = %q, want Automations", got)
	}
	app.Press("id:thread-2")
	app.Settle()
	if app.Exists("id:automations") || h.current != 2 {
		t.Fatalf("still on automations or not selected (current %d)", h.current)
	}
}

func (h *fakeHost) AddAttachments(paths []string) {
	h.attachments = append(h.attachments, paths...)
	uiruntime.Invalidate()
}
func (h *fakeHost) RemoveAttachment(path string) {
	var keep []string
	for _, p := range h.attachments {
		if p != path {
			keep = append(keep, p)
		}
	}
	h.attachments = keep
	uiruntime.Invalidate()
}
func (h *fakeHost) BackendReady() bool { return true }
func (h *fakeHost) CurrentAgent() string {
	if h.controls.id != "" {
		return h.controls.id
	}
	return model.AgentCodex
}
func (h *fakeHost) CurrentAgentControls() agent.Controls    { return &h.controls }
func (h *fakeHost) AgentSpeedBadge(string) string           { return "" }
func (h *fakeHost) CurrentPermissionPolicy() string         { return h.permission }
func (h *fakeHost) SetCurrentPermissionPolicy(v string)     { h.permission = v; uiruntime.Invalidate() }
func (h *fakeHost) Resume(model.Thread, *model.SendRequest) {}
func (h *fakeHost) RemoveQueued(threadID, queuedID int) {
	for i := range h.threads {
		if h.threads[i].ID != threadID {
			continue
		}
		var keep []model.QueuedMessage
		for _, q := range h.threads[i].Queue {
			if q.ID != queuedID {
				keep = append(keep, q)
			}
		}
		h.threads[i].Queue = keep
	}
	uiruntime.Invalidate()
}
func (h *fakeHost) ResumeQueue(int)                            { h.queuedActions = append(h.queuedActions, "resume") }
func (h *fakeHost) SteerQueued(int, int)                       { h.queuedActions = append(h.queuedActions, "steer") }
func (h *fakeHost) CanRewind(model.Thread, model.Message) bool { return h.canRewind }
func (h *fakeHost) Rewind(t, m int, text string) {
	h.rewound.threadID = t
	h.rewound.messageID = m
	h.rewound.text = text
}
func (h *fakeHost) PrefetchReference(ref model.ThreadRef) { h.prefetched = append(h.prefetched, ref) }
func (h *fakeHost) ThreadRefProjects() []model.ProjectSnapshot {
	return []model.ProjectSnapshot{{Path: h.state.Cwd, Threads: h.threads}}
}
func (h *fakeHost) SavedReferenceTitle(model.ThreadRef) (string, bool)    { return "", false }
func (h *fakeHost) RunThreadCommand(string) string                        { return "" }
func (h *fakeHost) TaskAction(int, string, string) string                 { return "" }
func (h *fakeHost) TaskWaitingApproval(model.Thread) bool                 { return false }
func (h *fakeHost) CollaborationCapabilities() *model.CollaborationStatus { return h.collab }
func (h *fakeHost) CollaborationUserOp(r appmodel.CollaborationRequest) (any, error) {
	if h.collaborationCall != nil {
		return h.collaborationCall(r)
	}
	return map[string]any{"tasks": []any{}}, nil
}

type fakeControls struct {
	id, model, effort, problem string
	reconnect                  bool
}

func (f *fakeControls) Name() string { return model.AgentName(f.id) }
func (f *fakeControls) Commands(string, bool) agent.CommandCatalog {
	return agent.DefaultCommands(model.AgentCodex)
}
func (f *fakeControls) Caps() agentmeta.Capabilities {
	return agentmeta.Capabilities{Models: true, Effort: true}
}
func (f *fakeControls) Problem() (string, bool)    { return f.problem, f.reconnect }
func (f *fakeControls) Reconnect()                 { f.problem = ""; uiruntime.Invalidate() }
func (f *fakeControls) ModelChoices() []string     { return []string{"default", "fast"} }
func (f *fakeControls) ModelLabel(s string) string { return s }
func (f *fakeControls) EffortChoices() []string    { return []string{"", "high"} }
func (f *fakeControls) Model() string              { return f.model }
func (f *fakeControls) Effort() string             { return f.effort }
func (f *fakeControls) SetModel(s string)          { f.model = s; uiruntime.Invalidate() }
func (f *fakeControls) SetEffort(s string)         { f.effort = s; uiruntime.Invalidate() }
