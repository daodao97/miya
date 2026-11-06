package integration

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/controller"
	projectmodel "miya/internal/project/model"
	svcproto "miya/internal/service/protocol"
	serviceserver "miya/internal/service/server"
	desktopui "miya/internal/ui"
)

func activityOrderFixture(t *testing.T, agent string) (*desktopui.App, *ui.Tester) {
	t.Helper()
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetThreads([]model.Thread{
		{ID: 1, Agent: agent, ServerID: "old", Title: "旧对话", LastActivityAt: 100, HistoryLoaded: true},
		{ID: 2, Agent: agent, ServerID: "recent", Title: "最近对话", LastActivityAt: 200, HistoryLoaded: true},
	})
	c.SetActive(2)
	c.SetProjects([]projectmodel.Project{{Name: "其他项目", Path: "/other"}, {Name: "demo", Path: c.Cwd}})
	c.ProjectCache = map[string][]model.Thread{
		c.Cwd:    c.Threads(),
		"/other": {{ID: 3, LastActivityAt: 300}},
	}
	return app, ui.NewTester(app.SidebarView, 260, 750)
}

func TestOpeningConversationDoesNotChangeActivityOrder(t *testing.T) {
	for _, agent := range []string{appmodel.AgentCodex, appmodel.AgentClaude} {
		t.Run(agent, func(t *testing.T) {
			app, tt := activityOrderFixture(t, agent)
			c := app.C.(*controller.Controller)
			beforeProjects := append([]projectmodel.Project(nil), c.Projects()...)
			tt.Frame()
			beforeOld, _ := tt.Find("旧对话")
			beforeRecent, _ := tt.Find("最近对话")
			if beforeOld.Y <= beforeRecent.Y {
				t.Fatal("fixture must put the older conversation below the recent one")
			}
			if err := tt.Click("旧对话"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			c.RememberDraft("只是写草稿，还没发送")
			tt.Frame()
			afterOld, _ := tt.Find("旧对话")
			afterRecent, _ := tt.Find("最近对话")
			if c.Active() != 1 || c.Current().LastActivityAt != 100 {
				t.Fatalf("opening a conversation changed activity: %+v", c.Current())
			}
			if beforeOld.Y != afterOld.Y || beforeRecent.Y != afterRecent.Y || !reflect.DeepEqual(c.Projects(), beforeProjects) {
				t.Fatal("opening or drafting reordered conversations or projects")
			}
		})
	}
}

func TestServiceOpenDoesNotChangeActivityOrder(t *testing.T) {
	for _, agent := range []string{appmodel.AgentCodex, appmodel.AgentClaude} {
		t.Run(agent, func(t *testing.T) {
			app, _ := activityOrderFixture(t, agent)
			c := app.C.(*controller.Controller)
			beforeProjects := append([]projectmodel.Project(nil), c.Projects()...)
			s := serviceserver.New(c, nil)
			if _, err := s.Handle(&serviceserver.Peer{}, svcproto.Request{Op: "open", Args: json.RawMessage(`1`)}); err != nil {
				t.Fatal(err)
			}
			shown := msgfmt.DisplayedThreads(c.Threads(), true)
			if shown[0].ID != 2 || shown[1].LastActivityAt != 100 || !reflect.DeepEqual(c.Projects(), beforeProjects) {
				t.Fatal("service open changed conversation activity or project order")
			}
		})
	}
}

func TestNewConversationTurnUpdatesActivityOrder(t *testing.T) {
	app, _ := activityOrderFixture(t, appmodel.AgentCodex)
	c := app.C.(*controller.Controller)
	c.SelectThread(c.ThreadWithID(1))
	c.Handle(event("turn/started", `{"threadId":"old","turn":{"id":"new-turn"}}`))
	if c.ThreadWithID(1).LastActivityAt <= 300 || msgfmt.DisplayedThreads(c.Threads(), true)[0].ID != 1 || c.Projects()[0].Path != c.Cwd {
		t.Fatal("a real conversation turn did not update conversation and project order")
	}
}
