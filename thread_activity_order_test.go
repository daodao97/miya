package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
)

func activityOrderFixture(t *testing.T, agent string) (*chatApp, *ui.Tester) {
	t.Helper()
	app, _ := testApp(t)
	c := app.c
	c.setThreads([]Thread{
		{ID: 1, Agent: agent, ServerID: "old", Title: "旧对话", LastActivityAt: 100, HistoryLoaded: true},
		{ID: 2, Agent: agent, ServerID: "recent", Title: "最近对话", LastActivityAt: 200, HistoryLoaded: true},
	})
	c.setActive(2)
	c.setProjects([]Project{{Name: "其他项目", Path: "/other"}, {Name: "demo", Path: c.cwd}})
	c.projectCache = map[string][]Thread{
		c.cwd:    c.threads(),
		"/other": {{ID: 3, LastActivityAt: 300}},
	}
	return app, ui.NewTester(app.sidebarView, 260, 750)
}

func TestOpeningConversationDoesNotChangeActivityOrder(t *testing.T) {
	for _, agent := range []string{agentCodex, agentClaude} {
		t.Run(agent, func(t *testing.T) {
			app, tt := activityOrderFixture(t, agent)
			c := app.c
			beforeProjects := append([]Project(nil), c.projects()...)
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
			c.rememberDraft("只是写草稿，还没发送")
			tt.Frame()
			afterOld, _ := tt.Find("旧对话")
			afterRecent, _ := tt.Find("最近对话")
			if c.active() != 1 || c.current().LastActivityAt != 100 {
				t.Fatalf("opening a conversation changed activity: %+v", c.current())
			}
			if beforeOld.Y != afterOld.Y || beforeRecent.Y != afterRecent.Y || !reflect.DeepEqual(c.projects(), beforeProjects) {
				t.Fatal("opening or drafting reordered conversations or projects")
			}
		})
	}
}

func TestServiceOpenDoesNotChangeActivityOrder(t *testing.T) {
	for _, agent := range []string{agentCodex, agentClaude} {
		t.Run(agent, func(t *testing.T) {
			app, _ := activityOrderFixture(t, agent)
			c := app.c
			beforeProjects := append([]Project(nil), c.projects()...)
			s := &service{c: c}
			if _, err := s.handle(&svcPeer{}, svcRequest{Op: "open", Args: json.RawMessage(`1`)}); err != nil {
				t.Fatal(err)
			}
			shown := displayedThreads(c.threads(), true)
			if shown[0].ID != 2 || shown[1].LastActivityAt != 100 || !reflect.DeepEqual(c.projects(), beforeProjects) {
				t.Fatal("service open changed conversation activity or project order")
			}
		})
	}
}

func TestNewConversationTurnUpdatesActivityOrder(t *testing.T) {
	app, _ := activityOrderFixture(t, agentCodex)
	c := app.c
	c.selectThread(c.threadWithID(1))
	c.handle(event("turn/started", `{"threadId":"old","turn":{"id":"new-turn"}}`))
	if c.threadWithID(1).LastActivityAt <= 300 || displayedThreads(c.threads(), true)[0].ID != 1 || c.projects()[0].Path != c.cwd {
		t.Fatal("a real conversation turn did not update conversation and project order")
	}
}
