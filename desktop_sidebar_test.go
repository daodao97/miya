package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSidebarActivityAcrossProjects(t *testing.T) {
	app, _ := testApp(t)
	c := app.c
	c.loadDesktopUnread()
	c.setProjects([]Project{{Name: "demo", Path: c.cwd}, {Name: "other", Path: "/other"}})
	current := []Thread{{ID: 1, Title: "当前运行", Busy: true, LastActivityAt: 20}, {ID: 2, Title: "加载历史", Busy: true, Loading: true}}
	for id := 3; id <= 9; id++ {
		current = append(current, Thread{ID: id, Title: fmt.Sprintf("待查看 %d", id), LastActivityAt: int64(id)})
		c.desktopUnread[desktopUnreadKey(current[len(current)-1])] = 100
	}
	c.setThreads(current)
	c.projectCache = map[string][]Thread{
		c.cwd: current, // must not duplicate the current project's entries
		"/other": {
			{ID: 10, Title: "其他项目运行", Busy: true, LastActivityAt: 30},
			{ID: 11, Title: "其他项目完成", Agent: agentClaude, ServerID: "claude-11", LastActivityAt: 40},
		},
	}
	c.desktopUnread[desktopUnreadKey(c.projectCache["/other"][1])] = 100
	running, completed := app.sidebarActivityThreads()
	if len(running) != 2 || running[0].ID != 10 || running[1].ID != 1 {
		t.Fatalf("wrong running conversations: %+v", running)
	}
	if len(completed) != 8 || completed[0].ID != 11 {
		t.Fatalf("completed conversations were duplicated, truncated or unsorted: %+v", completed)
	}
	app.expandedProject = ""
	tt := ui.NewTester(app.sidebarView, 260, 1000)
	tt.Frame()
	for _, label := range []string{"进行中", "已完成", "其他项目运行", "其他项目完成", "待查看 3"} {
		if !tt.HasText(label) {
			t.Fatalf("missing %q in %v", label, tt.Texts())
		}
	}
	for _, label := range []string{"进行中", "已完成"} {
		group, _ := tt.Find(label)
		projects, _ := tt.Find("Projects")
		if group.Y >= projects.Y {
			t.Fatal("activity groups should precede Projects")
		}
	}
	if _, ok := tt.Find("待读：其他项目完成"); !ok {
		t.Fatal("unread completion has no green dot")
	}
	c.desktopUnread = nil
	c.setThreads(nil)
	c.projectCache = nil
	tt.Frame()
	if tt.HasText("进行中") || tt.HasText("已完成") {
		t.Fatal("empty activity groups should be hidden")
	}
}

func TestDesktopUnreadCompletionPersistenceAndReadVersions(t *testing.T) {
	app, _ := testApp(t)
	c := app.c
	c.loadDesktopUnread()
	c.installNotifications()
	c.setThreads([]Thread{{ID: 1, ServerID: "session", Busy: true}, {ID: 2, Busy: true, Loading: true}})
	c.update(1, func(t *Thread) { t.Busy, t.CompletedAtMs = false, 100 })
	c.update(2, func(t *Thread) { t.Busy, t.Loading, t.CompletedAtMs = false, false, 100 })
	finished := c.threadWithID(1)
	if !c.desktopThreadUnread(finished) || c.desktopThreadUnread(c.threadWithID(2)) {
		t.Fatal("completion should be unread; loaded history should not")
	}
	c.projectCache = map[string][]Thread{"/other": {{ID: 3, ServerID: "background", Busy: true}}}
	c.update(3, func(t *Thread) { t.Busy, t.CompletedAtMs = false, 150 })
	if !c.desktopThreadUnread(c.threadWithID(3)) {
		t.Fatal("background completion was not recorded")
	}
	restored := &chatController{}
	restored.loadDesktopUnread()
	remapped := finished
	remapped.ID = 77
	if !restored.desktopThreadUnread(remapped) {
		t.Fatal("restart or reassigned sidebar ID lost unread state")
	}
	otherAgent := remapped
	otherAgent.Agent = agentClaude
	if restored.desktopThreadUnread(otherAgent) {
		t.Fatal("unread state leaked across backends")
	}
	c.update(1, func(t *Thread) { t.Busy = true })
	c.update(1, func(t *Thread) { t.Busy, t.CompletedAtMs = false, 200 })
	c.markDesktopRead(desktopRead{Key: desktopUnreadKey(finished), Version: 100})
	if !c.desktopThreadUnread(finished) {
		t.Fatal("stale read acknowledgement cleared a newer completion")
	}
	c.readDesktopThread(finished)
	if c.desktopThreadUnread(finished) {
		t.Fatal("opening the completed conversation did not clear unread state")
	}
	restored.loadDesktopUnread()
	if restored.desktopThreadUnread(remapped) {
		t.Fatal("read state did not persist")
	}
}

func TestDesktopUnreadServiceSnapshotAndAcknowledgement(t *testing.T) {
	app, _ := testApp(t)
	c := app.c
	c.loadDesktopUnread()
	thread := Thread{ID: 2, ServerID: "background", Agent: agentClaude}
	c.setProjects(append(c.projects(), Project{Name: "other", Path: "/other"}))
	c.projectCache = map[string][]Thread{"/other": {thread}}
	c.recordDesktopCompletion(Thread{Busy: true}, Thread{ID: 2, ServerID: thread.ServerID, Agent: thread.Agent, CompletedAtMs: 100})
	data, err := json.Marshal(c.snapshotState())
	if err != nil {
		t.Fatal(err)
	}
	var state svcState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	mirror := newMirrorController()
	t.Cleanup(mirror.cancel)
	mirror.applyState(state)
	if !mirror.desktopThreadUnread(thread) {
		t.Fatal("service did not send background unread state to the window")
	}
	key := desktopUnreadKey(thread)
	mirror.desktopReadPending = map[string]int64{key: 100}
	mirror.applyState(state)
	if mirror.desktopThreadUnread(thread) {
		t.Fatal("in-flight acknowledgement reappeared after an older snapshot")
	}
	args, _ := json.Marshal(desktopRead{Key: key, Version: 100})
	s := &service{c: c, dirty: make(chan struct{}, 1)}
	if _, err := s.handle(nil, svcRequest{Op: "desktop-read", Args: args}); err != nil {
		t.Fatal(err)
	}
	mirror.applyState(c.snapshotState())
	if mirror.desktopThreadUnread(thread) || len(mirror.desktopReadPending) != 0 {
		t.Fatal("service did not acknowledge read state")
	}
	state.DesktopUnread[key] = 200
	mirror.applyState(state)
	if !mirror.desktopThreadUnread(thread) {
		t.Fatal("a newer completion should become unread again")
	}
}

func TestSidebarActivityOpensOtherProject(t *testing.T) {
	app, _ := testApp(t)
	c := app.c
	// This test exercises navigation using cached threads, without discovery IO.
	c.connecting = true
	c.run = func(func()) {}
	other := t.TempDir()
	c.setProjects(append(c.projects(), Project{Name: "other", Path: other}))
	thread := Thread{ID: 2, Title: "跨项目会话", Busy: true, LastActivityAt: 50}
	c.projectCache = map[string][]Thread{other: {thread}}
	before := append([]Project(nil), c.projects()...)
	tt := ui.NewTester(app.sidebarView, 260, 750)
	if err := tt.Click(thread.Title); err != nil {
		t.Fatal(err)
	}
	if c.cwd != other || c.active() != thread.ID || app.workspace != other || app.expandedProject != other || app.projectName != "other" {
		t.Fatal("activity row did not open the conversation in its project")
	}
	if !reflect.DeepEqual(before, c.projects()) || c.current().LastActivityAt != 50 {
		t.Fatal("viewing a conversation changed activity order")
	}
}

func TestDesktopCompletionStaysUnreadUntilSelected(t *testing.T) {
	app, _ := testApp(t)
	c := app.c
	c.loadDesktopUnread()
	c.installNotifications()
	c.setThreads([]Thread{{ID: 1, ServerID: "session", Title: "完成后保留待读", Busy: true, HistoryLoaded: true}})
	app.selectThread(c.threadWithID(1))
	c.update(1, func(t *Thread) { t.Busy, t.CompletedAtMs = false, 100 })
	// A focused window can repaint many times while this thread remains open.
	// None of those frames is a user acknowledgement of the new result.
	for i := 0; i < 3; i++ {
		app.markRequestedThreadRead()
	}
	tt := ui.NewTester(app.sidebarView, 260, 750)
	tt.Frame()
	if !c.desktopThreadUnread(c.current()) || !tt.HasText("已完成") {
		t.Fatal("completion vanished before the user chose to view it")
	}
	if _, ok := tt.Find("待读：完成后保留待读"); !ok {
		t.Fatal("the finished conversation should retain its unread dot")
	}
	if err := tt.Click("完成后保留待读"); err != nil {
		t.Fatal(err)
	}
	app.markRequestedThreadRead()
	tt.Frame()
	if c.desktopThreadUnread(c.current()) || tt.HasText("已完成") {
		t.Fatal("explicitly viewing the completed result should clear unread")
	}
}

func TestDesktopReadIntentWaitsForHistoryAndKeepsNewerCompletion(t *testing.T) {
	app, _ := testApp(t)
	c := app.c
	c.loadDesktopUnread()
	c.installNotifications()
	c.setThreads([]Thread{{ID: 1, ServerID: "session", CompletedAtMs: 100}})
	c.setActive(1)
	c.desktopUnread[desktopUnreadKey(c.current())] = 100
	app.selectThread(c.current())
	app.markRequestedThreadRead()
	if !c.desktopThreadUnread(c.current()) {
		t.Fatal("history must arrive before acknowledging a selected result")
	}
	c.update(1, func(t *Thread) { t.HistoryLoaded = true })
	app.section = "Settings"
	app.markRequestedThreadRead()
	if !c.desktopThreadUnread(c.current()) {
		t.Fatal("a settings page should not acknowledge hidden conversation content")
	}
	app.section = ""
	app.markRequestedThreadRead()
	if c.desktopThreadUnread(c.current()) {
		t.Fatal("loaded history was not acknowledged after selection")
	}
	c.update(1, func(t *Thread) { t.Busy = true })
	c.update(1, func(t *Thread) { t.Busy, t.CompletedAtMs = false, 200 })
	app.requestDesktopRead(c.current())
	c.update(1, func(t *Thread) { t.Busy = true })
	c.update(1, func(t *Thread) { t.Busy, t.CompletedAtMs = false, 300 })
	app.markRequestedThreadRead()
	if !c.desktopThreadUnread(c.current()) {
		t.Fatal("an earlier selection acknowledged a result that completed later")
	}
}
