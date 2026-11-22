package integration

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/controller"
	"miya/internal/interaction"
	mcpproto "miya/internal/mcp/protocol"
	phoneproto "miya/internal/phone/protocol"
	projectmodel "miya/internal/project/model"
	svcclient "miya/internal/service/client"
)

// A conversation that waits for the person is in that group only, before the
// running and the completed ones.
func TestWaitingConversationIsListedOnceAndFirst(t *testing.T) {
	threads := []model.Thread{
		{ID: 1, Busy: true, LastActivityAt: 10}, // an open approval while its turn runs
		{ID: 2, LastActivityAt: 20},             // a question in its card, unread
		{ID: 3, LastActivityAt: 30},             // a question in its card, read
		{ID: 4, Busy: true, LastActivityAt: 40},
		{ID: 5, LastActivityAt: 50}, // unread
		{ID: 6, Loading: true},      // its history is still loading
		{ID: 7},
	}
	projects := []model.ProjectSnapshot{{Threads: threads[:4]}, {Threads: threads[3:]}}
	waits := func(t model.Thread) bool { return t.ID <= 3 || t.ID == 6 }
	unread := func(t model.Thread) bool { return t.ID == 2 || t.ID == 5 }
	ids := func(threads []model.Thread) string {
		out := []int{}
		for _, thread := range threads {
			out = append(out, thread.ID)
		}
		return fmt.Sprint(out)
	}
	waiting, running, completed := msgfmt.SidebarActivityThreads(projects, waits, unread)
	if ids(waiting) != "[3 2 1]" || ids(running) != "[4]" || ids(completed) != "[5]" {
		t.Fatalf("waiting %s, running %s, completed %s", ids(waiting), ids(running), ids(completed))
	}
	// Without the list of an older desktop the two groups are as before.
	waiting, running, completed = msgfmt.SidebarActivityThreads(projects, nil, unread)
	if ids(waiting) != "[]" || ids(running) != "[4 1]" || ids(completed) != "[5 2]" {
		t.Fatalf("without a waiting list: waiting %s, running %s, completed %s", ids(waiting), ids(running), ids(completed))
	}
}

// A conversation that ended on a question stays listed after the person read
// it, until the conversation goes on.
func TestWaitingForAnAnswerOutlastsReading(t *testing.T) {
	agent := appmodel.AgentClaude
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	c.StartStateCards(filepath.Join(t.TempDir(), "status"))
	c.SetProjects([]projectmodel.Project{{Name: "demo", Path: c.Cwd}})
	ask := statusRequest(mcpproto.StatusArgs{Purpose: "修复登录过期", WaitingFor: "是否兼容旧令牌"})
	plain := statusRequest(mcpproto.StatusArgs{Purpose: "整理发布说明"})
	c.SetThreads([]model.Thread{
		{ID: 1, Agent: agent, ServerID: "asks", Title: "登录过期", Busy: true, LastActivityAt: 30, Messages: []model.Message{{ID: 10, Role: "user", Content: "修一下登录", SentAtMs: 1000}, statusCall(agent, ask, 11, "item-1")}},
		{ID: 2, Agent: agent, ServerID: "plain", Title: "发布说明", Busy: true, LastActivityAt: 20, Messages: []model.Message{statusCall(agent, plain, 21, "item-2")}},
		{ID: 3, ServerID: "runs", Title: "还在跑", Busy: true, LastActivityAt: 10},
	})
	writeStateCard(t, c, ask)
	writeStateCard(t, c, plain)
	if waiting := c.WaitingThreads(); len(waiting) != 0 {
		t.Fatalf("a turn that still runs is listed as waiting: %v", waiting)
	}
	for _, id := range []int{1, 2} {
		c.Update(id, func(th *model.Thread) {
			th.Busy = false
			th.Messages[len(th.Messages)-1].Status = "completed"
		})
	}
	asks := c.ThreadWithID(1)
	key := appmodel.DesktopUnreadKey(asks)
	waiting := c.WaitingThreads()
	if len(waiting) != 1 || waiting[key] != "是否兼容旧令牌" || !waiting.Has(asks) || waiting.Has(c.ThreadWithID(2)) {
		t.Fatalf("only the conversation that asked should wait: %v", waiting)
	}
	// The person reads it and does not answer.
	c.DesktopUnread = map[string]int64{key: 100}
	c.MarkDesktopRead(appmodel.DesktopRead{Key: key, Version: 100})
	if c.DesktopThreadUnread(asks) || !c.WaitingThreads().Has(asks) {
		t.Fatal("reading the question removed it from the waiting list")
	}
	app.ExpandedProject = ""
	tt := ui.NewTester(app.SidebarView, 260, 1000)
	tt.Frame()
	group, ok := tt.Find("Waiting for you")
	running, _ := tt.Find("In progress")
	projects, _ := tt.Find("Projects")
	if !ok || group.Y >= running.Y || running.Y >= projects.Y {
		t.Fatalf("the waiting group should come first, above the running one and Projects: %v", tt.Texts())
	}
	if !tt.HasText("登录过期") || tt.HasText("发布说明") || tt.HasText("Completed") {
		t.Fatalf("the waiting group lists the wrong conversations: %v", tt.Texts())
	}
	waitingThreads, runningThreads, completed := app.SidebarActivityThreads()
	if len(waitingThreads) != 1 || waitingThreads[0].ID != 1 || len(runningThreads) != 1 || runningThreads[0].ID != 3 || len(completed) != 0 {
		t.Fatalf("waiting %+v, running %+v, completed %+v", waitingThreads, runningThreads, completed)
	}
	// The person answers: the card is outdated, also once that turn ended
	// without a new card.
	c.Update(1, func(th *model.Thread) {
		th.Busy = true
		th.Messages = append(th.Messages, model.Message{ID: 13, Role: "user", Content: "兼容旧令牌", SentAtMs: time.Now().UnixMilli() + 1000})
	})
	c.Update(1, func(th *model.Thread) { th.Busy = false })
	tt.Frame()
	if waiting := c.WaitingThreads(); len(waiting) != 0 || tt.HasText("Waiting for you") {
		t.Fatalf("an outdated card still lists its conversation as waiting: %v", waiting)
	}
}

// A turn held up by an approval or a question of the agent waits for the
// person; it is not listed as running.
func TestWaitingOnAnOpenRequestIsNotListedAsRunning(t *testing.T) {
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetProjects([]projectmodel.Project{{Name: "demo", Path: c.Cwd}})
	c.SetThreads([]model.Thread{
		{ID: 1, ServerID: "t1", TurnID: "turn1", Title: "等审批", Busy: true, LastActivityAt: 20},
		{ID: 2, ServerID: "t2", Title: "还在跑", Busy: true, LastActivityAt: 10},
	})
	c.SetApprovals([]interaction.Request{{ID: json.RawMessage(`7`), ThreadID: "t1", TurnID: "turn1"}})
	waiting, running, _ := app.SidebarActivityThreads()
	if len(waiting) != 1 || waiting[0].ID != 1 || len(running) != 1 || running[0].ID != 2 {
		t.Fatalf("waiting %+v, running %+v", waiting, running)
	}
	if what, ok := c.WaitingThreads()[appmodel.DesktopUnreadKey(c.ThreadWithID(1))]; !ok || what != "" {
		t.Fatalf("an open request has no question of a card: %q %v", what, ok)
	}
	app.ExpandedProject = ""
	tt := ui.NewTester(app.SidebarView, 260, 1000)
	tt.Frame()
	group, ok := tt.Find("Waiting for you")
	runningGroup, _ := tt.Find("In progress")
	if !ok || group.Y >= runningGroup.Y {
		t.Fatalf("the held-up conversation is not listed first: %v", tt.Texts())
	}
	c.SetApprovals(nil)
	waiting, running, _ = app.SidebarActivityThreads()
	if len(waiting) != 0 || len(running) != 2 {
		t.Fatalf("after the answer: waiting %+v, running %+v", waiting, running)
	}
}

// The service keeps the cards; its windows get the list with the state.
func TestWaitingConversationsReachAWindowOfTheService(t *testing.T) {
	f := newServiceFixture(t)
	id := collaborationCaller(t, f, "", "wait-session")
	r := statusRequest(mcpproto.StatusArgs{Purpose: "核对发布清单", WaitingFor: "是否今天发布"})
	f.serv.Do(func() {
		call := statusCall(appmodel.AgentCodex, r, f.C.NextID(), "item-1")
		f.C.Update(id, func(th *model.Thread) { th.Messages = append(th.Messages, call) })
	})
	client, err := svcclient.Dial(f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var saved string
	if err := client.Call("mcpStateCard", mcpproto.Request{Tool: r.Tool, Arguments: r.Arguments}, &saved); err != nil {
		t.Fatalf("card not saved through the service: %q %v", saved, err)
	}
	// The turn ends on the question.
	f.serv.Do(func() {
		f.C.Update(id, func(th *model.Thread) {
			th.Busy = false
			th.Messages[len(th.Messages)-1].Status = "completed"
		})
	})
	window := f.window()
	t.Cleanup(func() { window.Cancel(); window.Remote.Close() })
	f.waitWindow("the window lists the conversation as waiting", func() bool {
		th := window.ThreadWithID(id)
		return th.ServerID != "" && window.WaitingThreads()[appmodel.DesktopUnreadKey(th)] == "是否今天发布"
	})
	// The person answers in the service's conversation.
	f.serv.Do(func() {
		answer := model.Message{ID: f.C.NextID(), Role: "user", Content: "今天发", SentAtMs: time.Now().UnixMilli() + 1000}
		f.C.Update(id, func(th *model.Thread) { th.Messages = append(th.Messages, answer) })
	})
	f.waitWindow("the window stops listing the answered conversation", func() bool {
		return len(window.WaitingThreads()) == 0
	})
}

// The phone gets the desktop's list and groups by it as the desktop does.
func TestWaitingConversationsReachThePhone(t *testing.T) {
	agent := appmodel.AgentClaude
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	c.StartStateCards(filepath.Join(t.TempDir(), "status"))
	c.SetProjects([]projectmodel.Project{{Name: "current", Path: c.Cwd}, {Name: "other", Path: "/other"}})
	ask := statusRequest(mcpproto.StatusArgs{Purpose: "核对发布清单", WaitingFor: "是否今天发布"})
	c.SetThreads([]model.Thread{
		{ID: 1, Agent: agent, ServerID: "asks", Title: "发布清单", Busy: true, LastActivityAt: 10, Messages: []model.Message{statusCall(agent, ask, 11, "item-1")}},
		{ID: 2, ServerID: "runs", Title: "还在跑", Busy: true, LastActivityAt: 20},
	})
	writeStateCard(t, c, ask)
	c.Update(1, func(th *model.Thread) {
		th.Busy = false
		th.Messages[0].Status = "completed"
	})
	asks := c.ThreadWithID(1)
	key := appmodel.DesktopUnreadKey(asks)
	// Unread as well: it is still listed once.
	c.DesktopUnread = map[string]int64{key: 100}
	m := testMobileApp()
	m.PushDisabled = true
	data, err := json.Marshal(c.PhoneSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	var state phoneproto.State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	m.ApplyPhoneState(state)
	if m.State.Waiting[key] != "是否今天发布" {
		t.Fatalf("the phone did not get what the conversation waits for: %v", m.State.Waiting)
	}
	ids := func(threads []model.Thread) string {
		out := []int{}
		for _, thread := range threads {
			out = append(out, thread.ID)
		}
		return fmt.Sprint(out)
	}
	waiting, running, completed := app.SidebarActivityThreads()
	phoneWaiting, phoneRunning, phoneCompleted := m.SidebarActivityThreads()
	if ids(waiting) != "[1]" || ids(running) != "[2]" || ids(completed) != "[]" {
		t.Fatalf("desktop: waiting %s, running %s, completed %s", ids(waiting), ids(running), ids(completed))
	}
	if ids(phoneWaiting) != ids(waiting) || ids(phoneRunning) != ids(running) || ids(phoneCompleted) != ids(completed) {
		t.Fatalf("phone: waiting %s, running %s, completed %s", ids(phoneWaiting), ids(phoneRunning), ids(phoneCompleted))
	}
	// An older desktop sends no list: the phone groups as before.
	m.State.Waiting = nil
	phoneWaiting, _, phoneCompleted = m.SidebarActivityThreads()
	if ids(phoneWaiting) != "[]" || ids(phoneCompleted) != "[1]" {
		t.Fatalf("without a list: waiting %s, completed %s", ids(phoneWaiting), ids(phoneCompleted))
	}
}

func TestMobileDrawerWaitingGroupComesFirst(t *testing.T) {
	m := testMobileApp()
	first, second, done := mobileActivityFixture()
	m.State.Projects[0].Threads = []model.Thread{first}
	m.State.Projects[1].Threads = []model.Thread{second, done}
	m.State.CompletionUnread = map[string]int64{appmodel.DesktopUnreadKey(done): 100}
	m.State.Waiting = map[string]string{appmodel.DesktopUnreadKey(done): "是否今天发布"}
	m.PushDisabled = true
	tt := ui.NewTester(m.ViewRoot, 390, 844)
	tt.Frame()
	openMobileDrawer(t, tt)
	waitingHeader, waiting := tt.Find("Waiting for you")
	runningHeader, running := tt.Find("In progress")
	projectsHeader, _ := tt.Find("Projects")
	if !waiting || !running || waitingHeader.Y >= runningHeader.Y || runningHeader.Y >= projectsHeader.Y {
		t.Fatal("the waiting group should come first, above the running one and the project list")
	}
	if _, ok := tt.Find("Completed"); ok {
		t.Fatal("a waiting conversation is also listed as completed")
	}
	// The group shows when it is the only one with rows.
	m.State.Projects[0].Threads[0].Busy = false
	m.State.Projects[1].Threads[0].Busy = false
	tt.Frame()
	if _, ok := tt.Find("Waiting for you"); !ok {
		t.Fatal("the waiting group is hidden when nothing runs")
	}
	if _, ok := tt.Find("In progress"); ok {
		t.Fatal("the empty running group should be hidden")
	}
	if err := tt.Click(done.Title); err != nil {
		t.Fatal(err)
	}
	if m.Open != done.ID || m.Selected != "/b" || m.Menu {
		t.Fatal("a waiting row should open its conversation in its own project and close the drawer")
	}
}
