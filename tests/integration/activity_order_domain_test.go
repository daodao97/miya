package integration

import (
	"encoding/json"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/controller"
	projectmodel "miya/internal/project/model"
	svcproto "miya/internal/service/protocol"
	serviceserver "miya/internal/service/server"
	"reflect"
	"testing"
)

func TestNSGoServiceOpenDoesNotChangeActivityOrder(t *testing.T) {
	for _, agent := range []string{appmodel.AgentCodex, appmodel.AgentClaude} {
		t.Run(agent, func(t *testing.T) {
			c := activityOrderController(t, agent)
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

func TestNSGoNewConversationTurnUpdatesActivityOrder(t *testing.T) {
	c := activityOrderController(t, appmodel.AgentCodex)
	c.SelectThread(c.ThreadWithID(1))
	c.Handle(event("turn/started", `{"threadId":"old","turn":{"id":"new-turn"}}`))
	if c.ThreadWithID(1).LastActivityAt <= 300 || msgfmt.DisplayedThreads(c.Threads(), true)[0].ID != 1 || c.Projects()[0].Path != c.Cwd {
		t.Fatal("a real conversation turn did not update conversation and project order")
	}
}

func activityOrderController(t *testing.T, agent string) *controller.Controller {
	t.Helper()
	c := isolatedController(t)
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
	return c
}
