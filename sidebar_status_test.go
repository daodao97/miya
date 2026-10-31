package main

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestCollapsedProjectShowsRunningConversation(t *testing.T) {
	app, _ := testApp(t)
	app.expandedProject = ""
	app.c.setThreads([]Thread{{ID: 1, Busy: true}})
	tt := ui.NewTester(app.sidebarView, 260, 750)
	tt.Frame()
	if _, ok := tt.Find("项目进行中：demo"); !ok {
		t.Fatal("a collapsed project with a running conversation should show its spinner")
	}
	app.expandedProject = app.workspace
	tt.Frame()
	if _, ok := tt.Find("项目进行中：demo"); ok {
		t.Fatal("expanded projects show status on the individual conversation")
	}
	app.expandedProject = ""
	app.c.setThreads([]Thread{{ID: 1}})
	tt.Frame()
	if _, ok := tt.Find("项目进行中：demo"); ok {
		t.Fatal("a finished conversation should restore the collapse arrow")
	}
}

func TestProjectRunningStateReachesServiceWindow(t *testing.T) {
	app, _ := testApp(t)
	c := app.c
	c.setProjects([]Project{{Name: "demo", Path: c.cwd}, {Name: "background", Path: "/background"}})
	c.projectCache = map[string][]Thread{"/background": {{ID: 2, Agent: agentClaude, Busy: true}}}
	c.setThreads([]Thread{{ID: 1, Busy: true, Loading: true}})
	mirror := newMirrorController()
	t.Cleanup(mirror.cancel)
	apply := func() {
		t.Helper()
		data, err := json.Marshal(c.snapshotState())
		if err != nil {
			t.Fatal(err)
		}
		var state svcState
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatal(err)
		}
		mirror.applyState(state)
	}
	apply()
	if !mirror.projectBusy("/background") || mirror.projectBusy(c.cwd) {
		t.Fatal("background runs should reach the window; loading history should not count as running")
	}
	c.projectCache["/background"][0].Busy = false
	apply()
	if mirror.projectBusy("/background") {
		t.Fatal("the window should clear status after the background run finishes")
	}
}
