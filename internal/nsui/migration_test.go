package nsui

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
	"miya/internal/agent/cliupdate"
	"miya/internal/agent/quota"
	"miya/internal/appmodel"
	"miya/internal/async"
	"miya/internal/chat/model"
	"miya/internal/memory"
	"miya/internal/memory/manager"
	"miya/internal/nswidgets"
	"miya/internal/selfupdate"
	svcproto "miya/internal/service/protocol"
	"miya/internal/uiruntime"
)

type migrationHost struct {
	*fakeHost
	stops     int
	upgrades  []string
	updates   map[string]cliupdate.Info
	updating  bool
	taskCalls []string
}

func (h *migrationHost) Stop()                                   { h.stops++ }
func (h *migrationHost) AgentUpdates() map[string]cliupdate.Info { return h.updates }
func (h *migrationHost) AgentUpdateBusy() bool                   { return h.updating }
func (h *migrationHost) UpgradeAgent(id string) {
	h.upgrades = append(h.upgrades, id)
	h.updating = true
	uiruntime.Invalidate()
}
func (h *migrationHost) TaskAction(thread int, id, action string) string {
	h.taskCalls = append(h.taskCalls, fmt.Sprintf("%d:%s:%s", thread, id, action))
	return ""
}

func (h *migrationHost) PrepareTaskAction(thread int, id, action string) any {
	h.TaskAction(thread, id, action)
	return async.Reply(func() (any, error) { return "", nil })
}

func TestMigratedShellShortcutsAndPalette(t *testing.T) {
	h := &migrationHost{fakeHost: newFakeHost(model.Thread{ID: 1, Title: "Alpha", Busy: true}, model.Thread{ID: 2, Title: "Beta"})}
	app := startBridged(t, h)
	app.SetEnvironment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop})
	app.Key("ctrl+tab")
	if h.current != 2 {
		t.Fatal("conversation cycle did not select next")
	}
	app.Key("mod+.")
	if h.stops != 0 {
		t.Fatal("stopped an idle conversation")
	}
	app.Key("ctrl+shift+tab")
	app.Key("mod+.")
	if h.stops != 1 {
		t.Fatal("stop did not target current turn")
	}
	app.Key("mod+shift+p")
	app.Type("label:Search commands", "toggle sidebar")
	app.Key("enter")
	if app.Exists("id:command-palette") {
		t.Fatal("palette remained open after command")
	}
	app.Key("mod+k")
	app.Type("label:Search commands", "Beta")
	app.Key("enter")
	if h.current != 2 {
		t.Fatal("palette did not navigate")
	}
	app.Key("mod+k")
	app.Key("escape")
	if app.Exists("id:command-palette") {
		t.Fatal("escape did not dismiss palette")
	}
	app.Key("f1")
	if !app.Exists("id:shortcuts") {
		t.Fatal("shortcut guide missing")
	}
	app.Key("escape")
}

func TestMigratedPaletteSearchFindsLaterConversation(t *testing.T) {
	var threads []model.Thread
	for i := 1; i <= 40; i++ {
		threads = append(threads, model.Thread{ID: i, Title: fmt.Sprintf("Conversation %02d", i)})
	}
	h := newFakeHost(threads...)
	app := startBridged(t, h)
	app.SetEnvironment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop})
	app.Key("mod+k")
	app.Type("label:Search commands", "Conversation 39")
	app.Key("enter")
	if h.current != 39 {
		t.Fatalf("later result inaccessible: %d", h.current)
	}
	app.Key("mod+k")
	app.Type("label:Search commands", "Open settings")
	app.Key("enter")
	if !app.Exists("id:settings-dialog") {
		t.Fatal("palette could not open settings")
	}
}

func TestMigratedReadRequiresVisibleCurrentHistoryAndFocus(t *testing.T) {
	th := model.Thread{ID: 1, ServerID: "session", HistoryLoaded: true, CompletedAtMs: 100, Messages: []model.Message{{ID: 1, Role: "assistant", Content: "result"}}}
	h := newFakeHost(th)
	h.unread = map[int]bool{1: true}
	key := appmodel.DesktopUnreadKey(th)
	h.state.DesktopUnread = map[string]int64{key: 100}
	app := startBridged(t, h)
	app.SetEnvironment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop})
	r := readTracker{c: h}
	app.Post(func() {
		current.focused.Store(true)
		r.request(th)
		r.markVisible(false, true)
		r.markVisible(true, false) // an explicit open may read loaded history, even above the end
	})
	app.Settle()
	if len(h.readIDs) != 1 {
		t.Fatal("explicitly opened history remained unread")
	}
	app.Post(func() {
		h.unread[1] = true
		h.readIDs = nil
		r.intent = appmodel.DesktopRead{}
		r.markVisible(true, false)
		current.focused.Store(false)
		r.markVisible(true, true)
		current.focused.Store(true)
		current.minimized.Store(true)
		r.markVisible(true, true)
		current.minimized.Store(false)
		h.threads[0].Loading = true
		r.markVisible(true, true)
		h.threads[0].Loading = false
		h.threads[0].HistoryLoaded = false
		r.markVisible(true, true)
	})
	app.Settle()
	if len(h.readIDs) != 0 {
		t.Fatal("hidden, old, minimized, or unloaded content was read")
	}
	app.Post(func() { h.threads[0].HistoryLoaded = true; r.markVisible(true, true) })
	app.Settle()
	if len(h.readIDs) != 1 {
		t.Fatal("visible completed result remained unread")
	}
}

func TestMigratedReadIntentDoesNotConsumeNewerCompletion(t *testing.T) {
	th := model.Thread{ID: 1, ServerID: "session", CompletedAtMs: 100}
	h := newFakeHost(th)
	h.unread = map[int]bool{1: true}
	key := appmodel.DesktopUnreadKey(th)
	h.state.DesktopUnread = map[string]int64{key: 100}
	app := startBridged(t, h)
	app.SetEnvironment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop})
	r := readTracker{c: h}
	app.Post(func() {
		current.focused.Store(true)
		r.request(th)
		r.markVisible(true, false)
		h.state.DesktopUnread[key] = 200
		h.threads[0].HistoryLoaded = true
		r.markVisible(true, false)
	})
	app.Settle()
	if len(h.readIDs) != 0 {
		t.Fatal("earlier intent consumed a later result")
	}
	app.Post(func() { r.request(h.Current()); r.markVisible(true, false) })
	app.Settle()
	if len(h.readIDs) != 1 {
		t.Fatal("new explicit intent did not read history")
	}
}

func TestMigratedTaskActionsAndSubagentStayWithConversation(t *testing.T) {
	h := &migrationHost{fakeHost: newFakeHost(model.Thread{ID: 1, Busy: true, Tasks: []model.ThreadTask{{ID: "g", Kind: "goal", Status: "active", Title: "Ship tests"}, {ID: "loop", Kind: "loop", Status: "completed", Title: "Review"}}, Messages: []model.Message{{ID: 1, Role: "tool", Agents: []model.SubAgent{{ID: "child", Name: "Write tests", Status: "running"}}}}}, model.Thread{ID: 2})}
	app := startBridged(t, h)
	app.SetEnvironment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop})
	if !app.Exists("id:active-subagent-child") {
		t.Fatal("running subagent missing above composer")
	}
	app.Press("id:task-pause-g")
	app.Press("id:task-clear-loop")
	if strings.Join(h.taskCalls, ",") != "1:g:pause,1:loop:clear" {
		t.Fatal(h.taskCalls)
	}
	app.Press("id:thread-2")
	if app.Exists("id:active-subagent-child") || app.Exists("id:task-pause-g") {
		t.Fatal("task UI leaked across conversation")
	}
}

func TestDesktopFollowUpActionsUseDurableTaskHandler(t *testing.T) {
	h := &migrationHost{fakeHost: newFakeHost(model.Thread{ID: 1, Tasks: []model.ThreadTask{
		{ID: "waiting", Kind: "follow_up", Status: "waiting"},
		{ID: "blocked", Kind: "follow_up", Status: "blocked"},
	}}, model.Thread{ID: 2, ReadOnly: true, Tasks: []model.ThreadTask{{ID: "readonly", Kind: "follow_up", Status: "waiting"}}})}
	app := startBridged(t, h)
	app.Press("id:task-cancel-waiting")
	app.Press("id:task-dismiss-blocked")
	if strings.Join(h.taskCalls, ",") != "1:waiting:cancel,1:blocked:dismiss" {
		t.Fatal(h.taskCalls)
	}
	app.Press("id:thread-2")
	if app.Exists("id:task-cancel-waiting") || app.Enabled("id:task-cancel-readonly") {
		t.Fatal("follow-up controls lost conversation or read-only scope")
	}
}

func TestDesktopMemoryNoticeLoadsOnRequestAndUndoesExactVersion(t *testing.T) {
	h := newFakeHost(model.Thread{ID: 1, Messages: []model.Message{{ID: 1, Role: "memory", Memory: &model.MemoryNotice{
		Explicit: true, Project: "/work", Scope: "project", ID: "entry", Version: 3,
	}}}})
	var reads atomic.Int32
	undone := false
	h.state.Remote = settingsTestService(t, func(r svcproto.Request) (any, error) {
		if r.Op != "memoryGet" && r.Op != "memoryForget" {
			return nil, nil
		}
		var req manager.Request
		if err := json.Unmarshal(r.Args, &req); err != nil {
			return nil, err
		}
		if req.Project != "/work" || req.Scope != "project" || req.ID != "entry" || req.Version != 3 {
			return nil, fmt.Errorf("wrong memory request: %+v", req)
		}
		if r.Op == "memoryGet" {
			reads.Add(1)
		} else {
			undone = true
		}
		if undone {
			return memory.Entry{ID: "entry", Version: 4, State: memory.EntryForgotten}, nil
		}
		return memory.Entry{ID: "entry", Version: 3, State: memory.EntryActive, Content: "Remember to run the checks"}, nil
	})
	app := startBridged(t, h)
	if reads.Load() != 0 {
		t.Fatal("notice loaded memory before expanding")
	}
	app.Press("label:Miya memory changed · Current project")
	app.WaitFor("text:Remember to run the checks", time.Second)
	app.Press("id:memory-notice-undo-entry")
	app.WaitForGone("id:memory-notice-undo-entry", time.Second)
	if app.Exists("text:Remember to run the checks") || reads.Load() != 2 {
		t.Fatal("memory notice did not refresh after undo", app.Texts())
	}
}

func TestMigratedAgentUpgradeRequiresPressAndShowsProgress(t *testing.T) {
	h := &migrationHost{fakeHost: settingsHost(), updates: map[string]cliupdate.Info{"first": {Version: "1", Latest: "2", Update: true}}}
	app := startBridged(t, h)
	app.SetEnvironment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop})
	app.Press("label:Settings")
	if len(h.upgrades) != 0 {
		t.Fatal("opening settings upgraded CLI")
	}
	app.Press("id:upgrade-agent-first")
	if len(h.upgrades) != 1 || app.Enabled("id:upgrade-agent-first") {
		t.Fatal("upgrade not guarded")
	}
	app.Post(func() {
		h.updating = false
		h.updates["first"] = cliupdate.Info{Version: "2", Previous: "1"}
		uiruntime.Invalidate()
	})
	app.Settle()
	if !strings.Contains(strings.Join(app.Texts(), " "), "1 → 2") {
		t.Fatal(app.Texts())
	}
}

func TestMigratedRemoteUpdateRequiresPressAndVerifiesRelease(t *testing.T) {
	h := settingsHost()
	h.remote = true
	var installs atomic.Int32
	service := func(pid int, release string) func(svcproto.Request) (any, error) {
		return func(r svcproto.Request) (any, error) {
			if r.Op == "hello" {
				return svcproto.Hello{PID: pid, Release: release, SelfUpdate: true}, nil
			}
			if r.Op == "miyaUpdate" {
				var a struct{ Action, Version string }
				_ = json.Unmarshal(r.Args, &a)
				if a.Action == "install" {
					installs.Add(1)
					return selfupdate.Status{Supported: true, Current: release, Latest: "v2", Phase: "waiting"}, nil
				}
				return selfupdate.Status{Supported: true, Current: release, Latest: "v2", Phase: "available"}, nil
			}
			return nil, nil
		}
	}
	h.state.Remote = settingsTestService(t, service(10, "v1"))
	app, _ := startSettings(t, h, true)
	app.WaitFor("id:install-miya-update", time.Second)
	if installs.Load() != 0 {
		t.Fatal("opening settings installed update")
	}
	app.Press("id:install-miya-update")
	app.WaitUntil(time.Second, func() bool { return strings.Contains(strings.Join(app.Texts(), " "), "Waiting for tasks") })
	app.Post(func() { h.state.Remote = settingsTestService(t, service(11, "v2")); uiruntime.Invalidate() })
	app.Settle()
	app.WaitUntil(time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), " "), "running the requested Miya version")
	})
	if installs.Load() != 1 {
		t.Fatal("update installed more than once")
	}
}

func TestMigratedUsageTimeProgressEdgeCases(t *testing.T) {
	now := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		mins  int64
		reset time.Time
		want  float64
		valid bool
	}{{300, now.Add(207 * time.Minute), 31, true}, {10080, now.Add(84 * time.Hour), 50, true}, {300, now.Add(6 * time.Hour), 0, true}, {300, now, 100, true}, {300, now.Add(-time.Hour), 100, true}, {0, now, 0, false}, {-1, now, 0, false}, {300, time.Time{}, 0, false}} {
		reset := int64(0)
		if !tc.reset.IsZero() {
			reset = tc.reset.Unix()
		}
		got, ok := quota.TimeProgress(quota.Window{DurationMins: tc.mins, ResetsAt: reset}, now)
		if ok != tc.valid || math.Abs(got-tc.want) > 0.001 {
			t.Fatalf("%+v: %v %v", tc, got, ok)
		}
	}
	if usagePaceText(2, 31) != "Steady pace 31% · 29 points under" || usagePaceText(60, 40) != "Steady pace 40% · 20 points over" {
		t.Fatal("pace direction")
	}
}

func TestMigratedUsageTimeMarkerFitsAndMoves(t *testing.T) {
	var now *core.Signal[time.Time]
	at := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	w := quota.Window{Name: "5 hours", Used: 2, DurationMins: 300, ResetsAt: at.Add(207 * time.Minute).Unix()}
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		cx.SetTheme(nswidgets.Theme())
		now = core.NewSignal(cx, at)
		return core.Dynamic(func(cx *core.Scope) core.Node { return usageWindow(cx, w, now.Get(), "window") })
	}, nsgotest.Size(320, 400))
	track, marker := app.Find("id:window-progress"), app.Find("id:window-time")
	x := marker.Frame.Origin.X
	if x < track.Frame.Size.Width*.30 || x > track.Frame.Size.Width*.32 {
		t.Fatalf("marker %+v track %+v", marker.Frame, track.Frame)
	}
	app.Post(func() { now.Set(at.Add(30 * time.Minute)) })
	app.Settle()
	if app.Find("id:window-time").Frame.Origin.X <= x {
		t.Fatal("marker did not advance")
	}
}

func TestMigratedMessageLinksOpenOwnedPanelAndConversation(t *testing.T) {
	h := newFakeHost(model.Thread{ID: 1, ServerID: "source", HistoryLoaded: true, Messages: []model.Message{{ID: 1, Role: "assistant", Content: "[查看测试报告](docs/report.md)"}}}, model.Thread{ID: 2, ServerID: "target", Title: "Target conversation", HistoryLoaded: true})
	h.state.ReferenceSource = h.ThreadRef(h.threads[1]).Source
	files := newPanelFiles()
	files.texts["docs/report.md"] = "# 测试结果\n\nAll checks passed.\n"
	h.files = files
	app := startBridged(t, h)
	app.Press("text:查看测试报告")
	app.WaitFor("text:测试结果", time.Second)
	if !app.Exists("id:panel-tab-1") {
		t.Fatal("report did not open owned file tab")
	}
	app.Post(func() {
		h.threads[0].Messages = append(h.threads[0].Messages, model.Message{ID: 2, Role: "assistant", Content: "[Open linked thread](" + h.ThreadRef(h.threads[1]).URL() + ")"})
		uiruntime.Invalidate()
	})
	app.Settle()
	app.Press("text:Open linked thread")
	if h.current != 2 {
		t.Fatal("durable message link did not navigate")
	}
}

func TestMigratedCollaborationLateReplyStaysAtOriginalWorkspace(t *testing.T) {
	h := newFakeHost(model.Thread{ID: 1, ServerID: "source"})
	h.collab = &model.CollaborationStatus{Version: 1, Revision: 1}
	gate, entered := make(chan struct{}), make(chan struct{})
	var payloads []string
	var cancelled atomic.Int32
	original := settingsTestService(t, func(r svcproto.Request) (any, error) {
		var req appmodel.CollaborationRequest
		_ = json.Unmarshal(r.Args, &req)
		switch req.Operation {
		case "list_tasks":
			return map[string]any{"tasks": []any{map[string]any{"task_id": "task", "title": "Review", "state": "running"}}}, nil
		case "read_task":
			return map[string]any{"task_id": "task", "title": "Review", "state": "running"}, nil
		case "cancel_task":
			payloads = append(payloads, string(req.Arguments))
			if len(payloads) == 1 {
				close(entered)
				<-gate
			}
			return map[string]any{"accepted": true, "task_id": "task"}, nil
		}
		return nil, nil
	})
	other := settingsTestService(t, func(r svcproto.Request) (any, error) {
		var req appmodel.CollaborationRequest
		_ = json.Unmarshal(r.Args, &req)
		if req.Operation == "cancel_task" {
			cancelled.Add(1)
		}
		return map[string]any{"tasks": []any{}}, nil
	})
	h.state.Remote = original
	source := h.state.ReferenceSource
	panes := map[string]*CollaborationPanel{}
	t.Cleanup(func() { uiruntime.SetAppWindow(nil); current = nil })
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		installBridge(cx)
		cx.SetTheme(nswidgets.Theme())
		notice := core.NewSignal(cx, "")
		return collaborationStatus(cx, h, notice, panes)
	}, nsgotest.Size(900, 600))
	app.WaitFor("id:collaboration-toggle", time.Second)
	app.Press("id:collaboration-toggle")
	app.Press("id:collaboration-task-task")
	app.WaitFor("id:collaboration-cancel", time.Second)
	app.Press("id:collaboration-cancel")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("original service did not receive mutation")
	}
	app.Post(func() { h.state.Remote = other; h.state.ReferenceSource = "another-source"; uiruntime.Invalidate() })
	app.Settle()
	close(gate)
	// Retry is already visible (but disabled) while the request is in flight.
	// Wait for the late response to be rejected before restoring its source.
	app.WaitUntil(time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), "The connected workspace changed")
	})
	if cancelled.Load() != 0 {
		t.Fatal("mutation crossed into newly selected service")
	}
	app.Post(func() { h.state.Remote = original; h.state.ReferenceSource = source; uiruntime.Invalidate() })
	app.Settle()
	app.Press("id:collaboration-retry")
	app.WaitUntil(time.Second, func() bool { return !app.Exists("id:collaboration-retry") })
	if len(payloads) != 2 || payloads[0] != payloads[1] {
		t.Fatal("retry changed immutable request identity")
	}
}
