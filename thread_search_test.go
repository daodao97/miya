package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestGlobalThreadChoicesIncludeAllProjectsAndMatches(t *testing.T) {
	app, _ := testApp(t)
	c := app.c
	c.setActive(1)
	c.setThreads([]Thread{{ID: 1, ServerID: "session-1", Title: "登录自己"}, {ID: 2, ServerID: "session-2", Title: "登录当前", LastActivityAt: 100}})
	c.setProjects(append(c.projects(), Project{Name: "其他项目", Path: "/other"}))
	c.projectCache = map[string][]Thread{
		c.cwd:          {{ID: 2, ServerID: "session-2", Title: "旧标题"}, {ID: 99, ServerID: "session-99", Title: "登录已删除"}},
		"/other":       {{ID: 3, ServerID: "session-3", Agent: agentClaude, Title: "登录远程", LastActivityAt: 200}},
		"/cached-only": {{ID: 4, ServerID: "session-4", Title: "登录缓存"}},
	}
	for i := 5; i <= 12; i++ {
		c.projectCache["/other"] = append(c.projectCache["/other"], Thread{ID: i, ServerID: fmt.Sprintf("session-%d", i), Title: fmt.Sprintf("登录 %d", i)})
	}
	got := app.threadChoices("登录")
	if len(got) != 11 || got[0].Name != "登录远程" || got[1].Name != "登录当前" {
		t.Fatalf("global results missing, truncated or out of order: %+v", got)
	}
	if got[0].Detail != "Conversation · Claude Code · 其他项目" {
		t.Fatalf("project not identified: %+v", got[0])
	}
	if got := app.threadChoices("session-4"); len(got) != 1 || !strings.Contains(got[0].Detail, "cached-only") {
		t.Fatalf("cached project not found by ID: %+v", got)
	}
	if got := app.threadChoices("已删除"); len(got) != 0 {
		t.Fatalf("stale current-project cache searched: %+v", got)
	}
	if got := matchingThreadRefs([]phoneProject{{Threads: []Thread{{ID: 7, ServerID: "session-7", Title: "API Review"}, {ID: 7, ServerID: "session-7", Title: "API Review"}, {ID: 0}}}}, 0, "api"); len(got) != 1 {
		t.Fatalf("case-insensitive match or deduplication failed: %+v", got)
	}
}

func TestComposerCanChooseGlobalConversationBeyondFirstPage(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	c.setThreads([]Thread{{ID: 1, ServerID: "session-1", Title: "Current: ", HistoryLoaded: true}})
	c.setActive(1)
	c.projectCache = map[string][]Thread{"/other": {}}
	for i := 2; i <= 9; i++ {
		c.projectCache["/other"] = append(c.projectCache["/other"], Thread{ID: i, ServerID: fmt.Sprintf("session-%d", i), Title: fmt.Sprintf("跨项目 %d", i), HistoryLoaded: true, Messages: []Message{{Role: "user", Content: "上下文"}}})
	}
	tt.Frame()
	tt.Type("参考 @跨项目")
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
	if c.draft() != "参考 "+referenceText(testRef(agentCodex, "session-3"), "跨项目 3")+" " {
		t.Fatalf("wrong global reference inserted: %q", c.draft())
	}
	out, sources, err := expandThreadRefs(c.draft(), ThreadRef{}, c.resolveReference)
	if err != nil || len(sources) != 1 || !strings.Contains(out, "上下文") {
		t.Fatalf("global reference context not resolved: %q", out)
	}
}

func TestServiceMirrorsGlobalThreadSearchMetadata(t *testing.T) {
	c := serverController(t.TempDir(), "")
	c.setThreads([]Thread{{ID: 1, ServerID: "session-1", Title: "Current: "}})
	c.projectCache["/other"] = []Thread{{ID: 8, ServerID: "session-8", Title: "后台对话", Draft: "未发送", Queue: []QueuedMessage{{Text: "排队"}}, Messages: []Message{{Role: "user", Content: "完整正文"}}}}
	c.projectCache[c.cwd] = []Thread{{ID: 99, ServerID: "session-99", Title: "旧缓存"}}
	data, err := json.Marshal(c.snapshotState())
	if err != nil {
		t.Fatal(err)
	}
	var state svcState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	metadata := state.ProjectThreads["/other"]
	if len(metadata) != 1 || len(metadata[0].Messages) != 0 || metadata[0].Draft != "" || len(metadata[0].Queue) != 0 || len(state.ProjectThreads[c.cwd]) != 0 {
		t.Fatalf("snapshot did not send only background metadata: %+v", state.ProjectThreads)
	}
	if c.projectCache["/other"][0].Draft != "未发送" || len(c.projectCache["/other"][0].Messages) != 1 {
		t.Fatal("snapshot mutated service conversations")
	}
	window := newMirrorController()
	defer window.cancel()
	window.applyState(state)
	app := &chatApp{c: window}
	if got := app.threadChoices("后台"); len(got) != 1 || got[0].Name != "后台对话" {
		t.Fatalf("background thread did not reach window search: %+v", got)
	}
	delete(c.projectCache, "/other")
	window.applyState(c.snapshotState())
	if got := app.threadChoices("后台"); len(got) != 0 {
		t.Fatalf("removed background conversation remained searchable: %+v", got)
	}
}
