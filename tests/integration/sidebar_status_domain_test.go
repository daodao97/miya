package integration

import (
	"encoding/json"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/controller"
	projectmodel "miya/internal/project/model"
	svcproto "miya/internal/service/protocol"
	"testing"
)

func TestProjectRunningStateReachesServiceWindow(t *testing.T) {
	c := isolatedController(t)
	c.SetProjects([]projectmodel.Project{{Name: "demo", Path: c.Cwd}, {Name: "background", Path: "/background"}})
	c.ProjectCache = map[string][]model.Thread{"/background": {{ID: 2, Agent: appmodel.AgentClaude, Busy: true}}}
	c.SetThreads([]model.Thread{{ID: 1, Busy: true, Loading: true}})
	mirror := controller.NewMirrorController()
	t.Cleanup(mirror.Cancel)
	apply := func() {
		t.Helper()
		data, err := json.Marshal(c.SnapshotState())
		if err != nil {
			t.Fatal(err)
		}
		var state svcproto.State
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
