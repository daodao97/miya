package integration

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
	"miya/internal/controller"
	projectmodel "miya/internal/project/model"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
)

func sidebarNavigationState(project string, sameTime bool) svcproto.State {
	first := model.Thread{ID: 1, Title: "First running conversation", Busy: true, LastActivityAt: 10}
	second := model.Thread{ID: 2, Title: "Second running conversation", Busy: true, LastActivityAt: 20}
	if sameTime {
		second.LastActivityAt = first.LastActivityAt
	}
	state := svcproto.State{
		Cwd: project, Locale: "en",
		Projects:       []projectmodel.Project{{Name: "first", Path: "/first"}, {Name: "second", Path: "/second"}},
		ProjectThreads: map[string][]model.Thread{},
	}
	if project == "/first" {
		state.Threads, state.ProjectThreads["/second"] = []model.Thread{first}, []model.Thread{second}
	} else {
		state.Threads, state.ProjectThreads["/first"] = []model.Thread{second}, []model.Thread{first}
	}
	return state
}

func TestSidebarActivityStaysVisibleBetweenWorkspaceReplyAndSnapshot(t *testing.T) {
	app, _ := testApp(t)
	c := controller.NewMirrorController()
	t.Cleanup(c.Cancel)
	app.C = c
	c.ApplyState(sidebarNavigationState("/first", false))
	conn, peer := net.Pipe()
	c.Remote = svcclient.NewClient(conn)
	t.Cleanup(func() { c.Remote.Close(); peer.Close() })
	// Reply to navigation immediately, but leave its state push pending so
	// every intermediate frame can be inspected without a timing race.
	go func() {
		decoder, encoder := json.NewDecoder(peer), json.NewEncoder(peer)
		for {
			var request svcproto.Request
			if decoder.Decode(&request) != nil {
				return
			}
			if request.Op == "workspace" {
				var path string
				_ = json.Unmarshal(request.Args, &path)
				result, _ := json.Marshal(svcproto.WorkspaceResult{Cwd: path})
				if encoder.Encode(svcproto.Envelope{ID: request.ID, Result: result}) != nil {
					return
				}
			}
		}
	}()
	tt := ui.NewTester(app.SidebarView, 260, 750)
	positions := map[string]float32{}
	for _, title := range []string{"First running conversation", "Second running conversation"} {
		row, ok := tt.Find(title)
		if !ok {
			t.Fatalf("missing initial activity row %q", title)
		}
		positions[title] = row.Y
	}
	check := func(phase string) {
		t.Helper()
		tt.Frame()
		for title, y := range positions {
			if row, ok := tt.Find(title); !ok || row.Y != y {
				t.Fatalf("%s: activity row %q disappeared or moved: found=%v y=%v, want %v", phase, title, ok, row.Y, y)
			}
		}
	}
	for _, id := range []int{2, 1, 2, 1} {
		thread := c.ThreadWithID(id)
		if thread.ID == 0 {
			t.Fatalf("missing target conversation %d", id)
		}
		if err := tt.Click(thread.Title); err != nil {
			t.Fatal(err)
		}
		check("before state push")
		c.ApplyState(sidebarNavigationState(c.Cwd, false))
		check("after state push")
	}
}

func TestSidebarActivityEqualTimesKeepProjectOrder(t *testing.T) {
	app, _ := testApp(t)
	c := controller.NewMirrorController()
	t.Cleanup(c.Cancel)
	app.C = c
	for _, project := range []string{"/first", "/second", "/first"} {
		c.ApplyState(sidebarNavigationState(project, true))
		running, _ := app.SidebarActivityThreads()
		if len(running) != 2 || running[0].ID != 1 || running[1].ID != 2 {
			t.Fatalf("selecting %s changed activity order: %+v", project, running)
		}
	}
}
