package main

import (
	json "encoding/json"
	appmodel "miya/internal/appmodel"
	controller "miya/internal/controller"
	testing "testing"

	ui "github.com/egoist/mygo/ui"
)

func TestCollapsedProjectShowsRunningConversation(t *testing.T) {
	app, _ := testApp(t)
	app.ExpandedProject = ""
	app.C.(*controller.Controller).SetThreads([]appmodel.Thread{{ID: 1, Busy: true}})
	tt := ui.NewTester(app.SidebarView, 260, 750)
	tt.Frame()
	if _, ok := tt.Find("Project activity: demo"); !ok {
		t.Fatal("a collapsed project with a running conversation should show its spinner")
	}
	app.ExpandedProject = app.Workspace
	tt.Frame()
	if _, ok := tt.Find("Project activity: demo"); ok {
		t.Fatal("expanded projects show status on the individual conversation")
	}
	app.ExpandedProject = ""
	app.C.(*controller.Controller).SetThreads([]appmodel.Thread{{ID: 1}})
	tt.Frame()
	if _, ok := tt.Find("Project activity: demo"); ok {
		t.Fatal("a finished conversation should restore the collapse arrow")
	}
}

func TestProjectRunningStateReachesServiceWindow(t *testing.T) {
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetProjects([]appmodel.Project{{Name: "demo", Path: c.Cwd}, {Name: "background", Path: "/background"}})
	c.ProjectCache = map[string][]appmodel.Thread{"/background": {{ID: 2, Agent: appmodel.AgentClaude, Busy: true}}}
	c.SetThreads([]appmodel.Thread{{ID: 1, Busy: true, Loading: true}})
	mirror := controller.NewMirrorController()
	t.Cleanup(mirror.Cancel)
	apply := func() {
		t.Helper()
		data, err := json.Marshal(c.SnapshotState())
		if err != nil {
			t.Fatal(err)
		}
		var state appmodel.SvcState
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatal(err)
		}
		mirror.ApplyState(state)
	}
	apply()
	if !mirror.ProjectBusy("/background") || mirror.ProjectBusy(c.Cwd) {
		t.Fatal("background runs should reach the window; loading history should not count as running")
	}
	c.ProjectCache["/background"][0].Busy = false
	apply()
	if mirror.ProjectBusy("/background") {
		t.Fatal("the window should clear status after the background run finishes")
	}
}
