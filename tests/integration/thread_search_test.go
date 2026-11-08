package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/controller"
	projectmodel "miya/internal/project/model"
	svcproto "miya/internal/service/protocol"
	desktopui "miya/internal/ui"
)

func TestGlobalThreadChoicesIncludeAllProjectsAndMatches(t *testing.T) {
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetActive(1)
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "session-1", Title: "登录自己"}, {ID: 2, ServerID: "session-2", Title: "登录当前", LastActivityAt: 100}})
	c.SetProjects(append(c.Projects(), projectmodel.Project{Name: "其他项目", Path: "/other"}))
	c.ProjectCache = map[string][]model.Thread{
		c.Cwd:          {{ID: 2, ServerID: "session-2", Title: "旧标题"}, {ID: 99, ServerID: "session-99", Title: "登录已删除"}},
		"/other":       {{ID: 3, ServerID: "session-3", Agent: appmodel.AgentClaude, Title: "登录远程", LastActivityAt: 200}},
		"/cached-only": {{ID: 4, ServerID: "session-4", Title: "登录缓存"}},
	}
	for i := 5; i <= 12; i++ {
		c.ProjectCache["/other"] = append(c.ProjectCache["/other"], model.Thread{ID: i, ServerID: fmt.Sprintf("session-%d", i), Title: fmt.Sprintf("登录 %d", i)})
	}
	got := app.ThreadChoices("登录")
	if len(got) != 11 || got[0].Name != "登录远程" || got[1].Name != "登录当前" {
		t.Fatalf("global results missing, truncated or out of order: %+v", got)
	}
	if got[0].Detail != "Conversation · Claude Code · 其他项目" {
		t.Fatalf("project not identified: %+v", got[0])
	}
	if got := app.ThreadChoices("session-4"); len(got) != 1 || !strings.Contains(got[0].Detail, "cached-only") {
		t.Fatalf("cached project not found by ID: %+v", got)
	}
	if got := app.ThreadChoices("已删除"); len(got) != 0 {
		t.Fatalf("stale current-project cache searched: %+v", got)
	}
	if got := appmodel.MatchingThreadRefs([]model.ProjectSnapshot{{Threads: []model.Thread{{ID: 7, ServerID: "session-7", Title: "API Review"}, {ID: 7, ServerID: "session-7", Title: "API Review"}, {ID: 0}}}}, 0, "api"); len(got) != 1 {
		t.Fatalf("case-insensitive match or deduplication failed: %+v", got)
	}
}

func TestComposerCanChooseGlobalConversationBeyondFirstPage(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	// Deliver the asynchronous file search on this test's rendering goroutine,
	// as a real window does, before checking the merged reference choices.
	callbacks := make(chan func(), 1)
	c.Run = func(f func()) { callbacks <- f }
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "session-1", Title: "Current: ", HistoryLoaded: true}})
	c.SetActive(1)
	c.ProjectCache = map[string][]model.Thread{"/other": {}}
	for i := 2; i <= 9; i++ {
		c.ProjectCache["/other"] = append(c.ProjectCache["/other"], model.Thread{ID: i, ServerID: fmt.Sprintf("session-%d", i), Title: fmt.Sprintf("跨项目 %d", i), HistoryLoaded: true, Messages: []model.Message{{Role: "user", Content: "上下文"}}})
	}
	tt.Frame()
	tt.Type("参考 @跨项目")
	select {
	case f := <-callbacks:
		f()
	case <-time.After(2 * time.Second):
		t.Fatal("file search did not dispatch its completion")
	}
	tt.Frame()
	for i := 0; i < 6; i++ {
		tt.Key(0, ui.KeyDown)
	}
	tt.Frame()
	if !tt.HasText("跨项目 3") {
		t.Fatalf("later results unreachable: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if c.Draft() != "参考 "+model.ReferenceText(testRef(appmodel.AgentCodex, "session-3"), "跨项目 3")+" " {
		t.Fatalf("wrong global reference inserted: %q", c.Draft())
	}
	out, sources, err := controller.ExpandThreadRefs(c.Draft(), model.ThreadRef{}, c.ResolveReference)
	if err != nil || len(sources) != 1 || !strings.Contains(out, "上下文") {
		t.Fatalf("global reference context not resolved: %q", out)
	}
}

func TestServiceMirrorsGlobalThreadSearchMetadata(t *testing.T) {
	c := serverController(t.TempDir(), "")
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "session-1", Title: "Current: "}})
	c.ProjectCache["/other"] = []model.Thread{{ID: 8, ServerID: "session-8", Title: "后台对话", Draft: "未发送", Queue: []model.QueuedMessage{{SendRequest: model.SendRequest{Text: "排队"}}}, Messages: []model.Message{{Role: "user", Content: "完整正文"}}}}
	c.ProjectCache[c.Cwd] = []model.Thread{{ID: 99, ServerID: "session-99", Title: "旧缓存"}}
	data, err := json.Marshal(c.SnapshotState())
	if err != nil {
		t.Fatal(err)
	}
	var state svcproto.State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	metadata := state.ProjectThreads["/other"]
	if len(metadata) != 1 || len(metadata[0].Messages) != 0 || metadata[0].Draft != "" || len(metadata[0].Queue) != 0 || len(state.ProjectThreads[c.Cwd]) != 0 {
		t.Fatalf("snapshot did not send only background metadata: %+v", state.ProjectThreads)
	}
	if c.ProjectCache["/other"][0].Draft != "未发送" || len(c.ProjectCache["/other"][0].Messages) != 1 {
		t.Fatal("snapshot mutated service conversations")
	}
	window := controller.NewMirrorController()
	defer window.Cancel()
	window.ApplyState(state)
	app := &desktopui.App{C: window}
	if got := app.ThreadChoices("后台"); len(got) != 1 || got[0].Name != "后台对话" {
		t.Fatalf("background thread did not reach window search: %+v", got)
	}
	delete(c.ProjectCache, "/other")
	window.ApplyState(c.SnapshotState())
	if got := app.ThreadChoices("后台"); len(got) != 0 {
		t.Fatalf("removed background conversation remained searchable: %+v", got)
	}
}
