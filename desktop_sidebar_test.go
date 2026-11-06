package main

import (
	json "encoding/json"
	fmt "fmt"
	appmodel "miya/internal/appmodel"
	controller "miya/internal/controller"
	reflect "reflect"
	testing "testing"

	ui "github.com/egoist/mygo/ui"
)

func TestSidebarActivityAcrossProjects(t *testing.T) {
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	c.LoadDesktopUnread()
	c.SetProjects([]appmodel.Project{{Name: "demo", Path: c.Cwd}, {Name: "other", Path: "/other"}})
	current := []appmodel.Thread{{ID: 1, Title: "当前运行", Busy: true, LastActivityAt: 20}, {ID: 2, Title: "加载历史", Busy: true, Loading: true}}
	for id := 3; id <= 9; id++ {
		current = append(current, appmodel.Thread{ID: id, Title: fmt.Sprintf("待查看 %d", id), LastActivityAt: int64(id)})
		c.DesktopUnread[appmodel.DesktopUnreadKey(current[len(current)-1])] = 100
	}
	c.SetThreads(current)
	c.ProjectCache = map[string][]appmodel.Thread{
		c.Cwd: current, // must not duplicate the current project's entries
		"/other": {
			{ID: 10, Title: "其他项目运行", Busy: true, LastActivityAt: 30},
			{ID: 11, Title: "其他项目完成", Agent: appmodel.AgentClaude, ServerID: "claude-11", LastActivityAt: 40},
		},
	}
	c.DesktopUnread[appmodel.DesktopUnreadKey(c.ProjectCache["/other"][1])] = 100
	running, completed := app.SidebarActivityThreads()
	if len(running) != 2 || running[0].ID != 10 || running[1].ID != 1 {
		t.Fatalf("wrong running conversations: %+v", running)
	}
	if len(completed) != 8 || completed[0].ID != 11 {
		t.Fatalf("completed conversations were duplicated, truncated or unsorted: %+v", completed)
	}
	app.ExpandedProject = ""
	tt := ui.NewTester(app.SidebarView, 260, 1000)
	tt.Frame()
	for _, label := range []string{"In progress", "Completed", "其他项目运行", "其他项目完成", "待查看 3"} {
		if !tt.HasText(label) {
			t.Fatalf("missing %q in %v", label, tt.Texts())
		}
	}
	for _, label := range []string{"In progress", "Completed"} {
		group, _ := tt.Find(label)
		projects, _ := tt.Find("Projects")
		if group.Y >= projects.Y {
			t.Fatal("activity groups should precede Projects")
		}
	}
	if _, ok := tt.Find("Unread: 其他项目完成"); !ok {
		t.Fatal("unread completion has no green dot")
	}
	c.DesktopUnread = nil
	c.SetThreads(nil)
	c.ProjectCache = nil
	tt.Frame()
	if tt.HasText("In progress") || tt.HasText("Completed") {
		t.Fatal("empty activity groups should be hidden")
	}
}

func TestDesktopUnreadCompletionPersistenceAndReadVersions(t *testing.T) {
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	c.LoadDesktopUnread()
	c.InstallNotifications()
	c.SetThreads([]appmodel.Thread{{ID: 1, ServerID: "session", Busy: true}, {ID: 2, Busy: true, Loading: true}})
	c.Update(1, func(t *appmodel.Thread) { t.Busy, t.CompletedAtMs = false, 100 })
	c.Update(2, func(t *appmodel.Thread) { t.Busy, t.Loading, t.CompletedAtMs = false, false, 100 })
	finished := c.ThreadWithID(1)
	if !c.DesktopThreadUnread(finished) || c.DesktopThreadUnread(c.ThreadWithID(2)) {
		t.Fatal("completion should be unread; loaded history should not")
	}
	c.ProjectCache = map[string][]appmodel.Thread{"/other": {{ID: 3, ServerID: "background", Busy: true}}}
	c.Update(3, func(t *appmodel.Thread) { t.Busy, t.CompletedAtMs = false, 150 })
	if !c.DesktopThreadUnread(c.ThreadWithID(3)) {
		t.Fatal("background completion was not recorded")
	}
	restored := &controller.Controller{}
	restored.LoadDesktopUnread()
	remapped := finished
	remapped.ID = 77
	if !restored.DesktopThreadUnread(remapped) {
		t.Fatal("restart or reassigned sidebar ID lost unread state")
	}
	otherAgent := remapped
	otherAgent.Agent = appmodel.AgentClaude
	if restored.DesktopThreadUnread(otherAgent) {
		t.Fatal("unread state leaked across backends")
	}
	c.Update(1, func(t *appmodel.Thread) { t.Busy = true })
	c.Update(1, func(t *appmodel.Thread) { t.Busy, t.CompletedAtMs = false, 200 })
	c.MarkDesktopRead(appmodel.DesktopRead{Key: appmodel.DesktopUnreadKey(finished), Version: 100})
	if !c.DesktopThreadUnread(finished) {
		t.Fatal("stale read acknowledgement cleared a newer completion")
	}
	c.ReadDesktopThread(finished)
	if c.DesktopThreadUnread(finished) {
		t.Fatal("opening the completed conversation did not clear unread state")
	}
	restored.LoadDesktopUnread()
	if restored.DesktopThreadUnread(remapped) {
		t.Fatal("read state did not persist")
	}
}

func TestDesktopUnreadServiceSnapshotAndAcknowledgement(t *testing.T) {
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	c.LoadDesktopUnread()
	thread := appmodel.Thread{ID: 2, ServerID: "background", Agent: appmodel.AgentClaude}
	c.SetProjects(append(c.Projects(), appmodel.Project{Name: "other", Path: "/other"}))
	c.ProjectCache = map[string][]appmodel.Thread{"/other": {thread}}
	c.RecordDesktopCompletion(appmodel.Thread{Busy: true}, appmodel.Thread{ID: 2, ServerID: thread.ServerID, Agent: thread.Agent, CompletedAtMs: 100})
	data, err := json.Marshal(c.SnapshotState())
	if err != nil {
		t.Fatal(err)
	}
	var state appmodel.SvcState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	mirror := controller.NewMirrorController()
	t.Cleanup(mirror.Cancel)
	mirror.ApplyState(state)
	if !mirror.DesktopThreadUnread(thread) {
		t.Fatal("service did not send background unread state to the window")
	}
	key := appmodel.DesktopUnreadKey(thread)
	mirror.DesktopReadPending = map[string]int64{key: 100}
	mirror.ApplyState(state)
	if mirror.DesktopThreadUnread(thread) {
		t.Fatal("in-flight acknowledgement reappeared after an older snapshot")
	}
	args, _ := json.Marshal(appmodel.DesktopRead{Key: key, Version: 100})
	s := &controller.Service{C: c, Dirty: make(chan struct{}, 1)}
	if _, err := s.Handle(nil, appmodel.SvcRequest{Op: "desktop-read", Args: args}); err != nil {
		t.Fatal(err)
	}
	mirror.ApplyState(c.SnapshotState())
	if mirror.DesktopThreadUnread(thread) || len(mirror.DesktopReadPending) != 0 {
		t.Fatal("service did not acknowledge read state")
	}
	state.DesktopUnread[key] = 200
	mirror.ApplyState(state)
	if !mirror.DesktopThreadUnread(thread) {
		t.Fatal("a newer completion should become unread again")
	}
}

func TestSidebarActivityOpensOtherProject(t *testing.T) {
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	// This test exercises navigation using cached threads, without discovery IO.
	c.Connecting = true
	c.Run = func(func()) {}
	other := t.TempDir()
	c.SetProjects(append(c.Projects(), appmodel.Project{Name: "other", Path: other}))
	thread := appmodel.Thread{ID: 2, Title: "跨项目会话", Busy: true, LastActivityAt: 50}
	c.ProjectCache = map[string][]appmodel.Thread{other: {thread}}
	before := append([]appmodel.Project(nil), c.Projects()...)
	tt := ui.NewTester(app.SidebarView, 260, 750)
	if err := tt.Click(thread.Title); err != nil {
		t.Fatal(err)
	}
	if c.Cwd != other || c.Active() != thread.ID || app.Workspace != other || app.ExpandedProject != other || app.ProjectName != "other" {
		t.Fatal("activity row did not open the conversation in its project")
	}
	if !reflect.DeepEqual(before, c.Projects()) || c.Current().LastActivityAt != 50 {
		t.Fatal("viewing a conversation changed activity order")
	}
}

func TestDesktopCompletionStaysUnreadWithoutDisplayingMessages(t *testing.T) {
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	c.LoadDesktopUnread()
	c.InstallNotifications()
	c.SetThreads([]appmodel.Thread{{ID: 1, ServerID: "session", Title: "完成后保留待读", Busy: true, HistoryLoaded: true}})
	app.SelectThread(c.ThreadWithID(1))
	c.Update(1, func(t *appmodel.Thread) { t.Busy, t.CompletedAtMs = false, 100 })
	// Sidebar-only frames do not prove that the result has been displayed.
	for i := 0; i < 3; i++ {
		app.MarkRequestedThreadRead()
	}
	tt := ui.NewTester(app.SidebarView, 260, 750)
	tt.Frame()
	if !c.DesktopThreadUnread(c.Current()) || !tt.HasText("Completed") {
		t.Fatal("completion vanished before the user chose to view it")
	}
	if _, ok := tt.Find("Unread: 完成后保留待读"); !ok {
		t.Fatal("the finished conversation should retain its unread dot")
	}
	if err := tt.Click("完成后保留待读"); err != nil {
		t.Fatal(err)
	}
	app.MarkRequestedThreadRead()
	tt.Frame()
	if c.DesktopThreadUnread(c.Current()) || tt.HasText("Completed") {
		t.Fatal("explicitly viewing the completed result should clear unread")
	}
}

func TestDesktopOpenCompletionBecomesReadAfterDisplayingLatest(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.LoadDesktopUnread()
	c.InstallNotifications()
	c.SetThreads([]appmodel.Thread{{ID: 1, ServerID: "session", Title: "当前对话", Busy: true, HistoryLoaded: true,
		Messages: []appmodel.Message{{ID: 10, Role: "user", Content: "请求"}, {ID: 11, Role: "assistant", Content: "回复"}}}})
	app.SelectThread(c.ThreadWithID(1))
	tt.Frame()
	c.Update(1, func(t *appmodel.Thread) { t.Busy, t.CompletedAtMs = false, 100 })
	// The previous frame still showed a running turn.
	app.MarkDisplayedThreadRead()
	if !c.DesktopThreadUnread(c.Current()) {
		t.Fatal("completion was acknowledged before its finished frame")
	}
	tt.Frame()
	if !app.ListFor(1).AtEnd() {
		t.Fatal("the completed result is not on screen")
	}
	// An unfocused/headless window cannot acknowledge it merely by repainting.
	app.MarkVisibleThreadRead()
	if !c.DesktopThreadUnread(c.Current()) {
		t.Fatal("a background window acknowledged the completion")
	}
	// Exercise the displayed-content check after the window focus guard.
	app.MarkDisplayedThreadRead()
	tt.Frame()
	if c.DesktopThreadUnread(c.Current()) || tt.HasText("Completed") {
		t.Fatal("the result already displayed at the end stayed unread")
	}
	if _, ok := tt.Find("Unread: 当前对话"); ok {
		t.Fatal("the viewed conversation still has a green unread dot")
	}
}

func TestDesktopDisplayedReadWaitsForLatestAndVisibleConversation(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.LoadDesktopUnread()
	c.SetThreads([]appmodel.Thread{{ID: 1, ServerID: "session", HistoryLoaded: true, CompletedAtMs: 100, Messages: scrollTestMessages()}})
	c.SetActive(1)
	key := appmodel.DesktopUnreadKey(c.Current())
	c.DesktopUnread[key] = 100
	tt.Frame()
	list := app.ListFor(1)
	list.ScrollTo(10, ui.Center)
	tt.Frame()
	app.MarkDisplayedThreadRead()
	if list.AtEnd() || !c.DesktopThreadUnread(c.Current()) {
		t.Fatal("reading older messages acknowledged an unseen latest result")
	}
	list.ScrollToEnd()
	tt.Frame()
	for _, hidden := range []string{"settings", "switcher", "shortcuts", "command-palette", "project-dialog", "mobile"} {
		app.Section = ""
		app.Switcher.Open, app.ShortcutsOpen, app.ProjectDialog, app.Mobile = false, false, false, false
		app.CommandPalette.Open = false
		switch hidden {
		case "settings":
			app.Section = "Settings"
		case "switcher":
			app.Switcher.Open = true
		case "shortcuts":
			app.ShortcutsOpen = true
		case "command-palette":
			app.CommandPalette.Open = true
		case "project-dialog":
			app.ProjectDialog = true
		case "mobile":
			app.Mobile = true
		}
		app.MarkDisplayedThreadRead()
		if !c.DesktopThreadUnread(c.Current()) {
			t.Fatalf("hidden conversation acknowledged by %s", hidden)
		}
	}
	app.Mobile = false
	// Metadata can publish before the new message contents arrive.
	c.DesktopUnread[key] = 200
	app.MarkDisplayedThreadRead()
	if !c.DesktopThreadUnread(c.Current()) {
		t.Fatal("an older displayed result acknowledged a newer completion")
	}
	c.Update(1, func(t *appmodel.Thread) { t.CompletedAtMs = 200 })
	app.MarkDisplayedThreadRead()
	if !c.DesktopThreadUnread(c.Current()) {
		t.Fatal("new completion metadata was acknowledged before rendering")
	}
	tt.Frame()
	app.MarkDisplayedThreadRead()
	if c.DesktopThreadUnread(c.Current()) {
		t.Fatal("reading the latest completed result did not clear unread")
	}
}

func TestDesktopReadIntentWaitsForHistoryAndKeepsNewerCompletion(t *testing.T) {
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	c.LoadDesktopUnread()
	c.InstallNotifications()
	c.SetThreads([]appmodel.Thread{{ID: 1, ServerID: "session", CompletedAtMs: 100}})
	c.SetActive(1)
	c.DesktopUnread[appmodel.DesktopUnreadKey(c.Current())] = 100
	app.SelectThread(c.Current())
	app.MarkRequestedThreadRead()
	if !c.DesktopThreadUnread(c.Current()) {
		t.Fatal("history must arrive before acknowledging a selected result")
	}
	c.Update(1, func(t *appmodel.Thread) { t.HistoryLoaded = true })
	app.Section = "Settings"
	app.MarkRequestedThreadRead()
	if !c.DesktopThreadUnread(c.Current()) {
		t.Fatal("a settings page should not acknowledge hidden conversation content")
	}
	app.Section = ""
	app.MarkRequestedThreadRead()
	if c.DesktopThreadUnread(c.Current()) {
		t.Fatal("loaded history was not acknowledged after selection")
	}
	c.Update(1, func(t *appmodel.Thread) { t.Busy = true })
	c.Update(1, func(t *appmodel.Thread) { t.Busy, t.CompletedAtMs = false, 200 })
	app.RequestDesktopRead(c.Current())
	c.Update(1, func(t *appmodel.Thread) { t.Busy = true })
	c.Update(1, func(t *appmodel.Thread) { t.Busy, t.CompletedAtMs = false, 300 })
	app.MarkRequestedThreadRead()
	if !c.DesktopThreadUnread(c.Current()) {
		t.Fatal("an earlier selection acknowledged a result that completed later")
	}
}
