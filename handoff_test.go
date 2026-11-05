package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"miya/internal/codex"
	"miya/internal/subagent"
)

func handoffSource() Thread {
	change := &codex.Item{}
	change.Changes = append(change.Changes, struct {
		Path string `json:"path"`
		Diff string `json:"diff"`
	}{Path: "auth/login.go", Diff: "+fix"})
	return Thread{ID: 2, ServerID: "source-session", Agent: agentClaude, Title: "登录修复", HistoryLoaded: true, Messages: []Message{
		{ID: 1, Role: "user", Content: "修复登录过期的问题"},
		{ID: 2, Role: "tool", Kind: "claudeTool", Content: "Edit\n{\"file_path\":\"auth/token.go\"}\nok", Status: "completed"},
		{ID: 3, Role: "tool", Kind: "fileChange", Item: change, Status: "completed"},
		{ID: 4, Role: "assistant", Content: "已修复：token 过期判断用错了时区。"},
		{ID: 5, Role: "tool", Kind: "claudeTool", Content: "Agent\n{}", Agents: []SubAgent{{ID: "t", Name: "跑测试", Status: subagent.Completed, Detail: "全部通过"}}},
	}}
}

func TestThreadRefsExpandIntoContextPackets(t *testing.T) {
	src := handoffSource()
	ref := testReference(src)
	resolve := func(r ThreadRef) (Thread, []Message, error) {
		if r == ref {
			return src, src.Messages, nil
		}
		return Thread{}, nil, fmt.Errorf("missing")
	}
	text := "参考 " + referenceText(ref, src.Title) + " 给登录页加上提示"
	if got := threadRefs(text); len(got) != 1 || got[0] != ref {
		t.Fatalf("bad refs: %v", got)
	}
	out, sources, err := expandThreadRefs(text, ThreadRef{}, resolve)
	if err != nil || len(sources) != 1 {
		t.Fatalf("bad expansion: %v %v", sources, err)
	}
	for _, want := range []string{text, "source-session", "修复登录过期的问题", "已修复：token 过期判断用错了时区。", "auth/token.go", "auth/login.go", "跑测试 · Completed · 全部通过"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
	if again, more, err := expandThreadRefs(out, ThreadRef{}, resolve); err != nil || again != out || len(more) != 0 {
		t.Fatal("snapshot was expanded again")
	}
	if got := threadContextDisplay(out); strings.Contains(got, "miya-thread-context") || !strings.Contains(got, "[Referenced conversation: 登录修复]") {
		t.Fatal(got)
	}
	if got := threadRefTask(text); got != "参考 给登录页加上提示" {
		t.Fatal(got)
	}
}

func TestUnloadedThreadReferenceFailsWithoutSending(t *testing.T) {
	src := handoffSource()
	src.Messages = nil
	src.HistoryLoaded = false
	raw := referenceText(testReference(src), src.Title)
	got, sources, err := expandThreadRefs(raw, ThreadRef{}, func(ThreadRef) (Thread, []Message, error) { return src, nil, nil })
	if err == nil || got != raw || len(sources) != 0 {
		t.Fatalf("unloaded reference accepted: %s %v", got, err)
	}
}

func TestThreadReferenceDoesNotChangeSourceConversation(t *testing.T) {
	for _, background := range []bool{false, true} {
		t.Run(fmt.Sprintf("background=%v", background), func(t *testing.T) {
			c := testController()
			c.active, c.setActive = createSignal(1)
			c.agent, c.setAgent = createSignal(agentClaude)
			c.cwd = "/open"
			src := handoffSource()
			target := Thread{ID: 1, Agent: agentClaude, ServerID: "target", Title: "登录页", Busy: true, HistoryLoaded: true}
			if background {
				c.setThreads([]Thread{target})
				c.projectCache = map[string][]Thread{"/other": {src}}
			} else {
				c.setThreads([]Thread{target, src})
			}
			if id := c.submit(SendRequest{ThreadID: 1, Text: "参考 " + referenceText(testReference(src), src.Title) + " 给登录页加上提示"}); id != 1 {
				t.Fatalf("submit returned %d", id)
			}
			target = c.threadWithID(1)
			if len(target.Queue) != 1 || !strings.Contains(target.Queue[0].Text, contextEnvelopeOpen) {
				t.Fatalf("queued message not expanded: %+v", target.Queue)
			}
			assertUnchanged := func() {
				t.Helper()
				if got := c.threadWithID(src.ID); !reflect.DeepEqual(got, src) || len(c.handoffs) != 0 {
					t.Fatalf("reference changed its source or created a handoff: %+v, %+v", got, c.handoffs)
				}
			}
			assertUnchanged()
			c.update(1, func(th *Thread) {
				th.Busy, th.Queue = false, nil
				th.Messages = append(th.Messages, Message{ID: c.nextID(), Role: "user", Content: "参考"}, Message{ID: c.nextID(), Role: "assistant", Content: "提示已加上"})
			})
			assertUnchanged()
		})
	}
}

func TestThreadChoiceInsertsReference(t *testing.T) {
	src := handoffSource()
	value := "看下 @登 然后修"
	token := composerTokenAt(value, len("看下 @登"))
	want := "看下 " + referenceText(testReference(src), src.Title) + "  然后修"
	if got := insertThreadReference(value, token, testReference(src), src.Title); got != want {
		t.Fatal(got)
	}
}

func TestComposerOffersConversationsAfterAt(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	src := handoffSource()
	c.setThreads([]Thread{{ID: 1, Agent: agentCodex, ServerID: "target", Title: "登录页提示", HistoryLoaded: true}, src})
	c.setActive(1)
	tt.Frame()
	tt.Type("参考 @登")
	tt.Frame()
	if !tt.HasText("登录修复") || !tt.HasText("Conversations and project files") || !tt.HasText("Conversation · Claude Code · demo") || tt.HasText("#1 登录页提示") {
		t.Fatalf("conversation choices missing or include the open one: %q", tt.Texts())
	}
	if dir := os.Getenv("MIYA_HANDOFF_SNAPSHOT"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "composer.png"))
		if err != nil {
			t.Fatal(err)
		}
		_ = png.Encode(f, tt.Image())
		f.Close()
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if got := c.draft(); got != "参考 "+referenceText(testReference(src), src.Title)+" " {
		t.Fatalf("choosing a conversation drafted %q", got)
	}
	app.handoffToNewThread()
	if c.active() != 0 || c.draft() != referenceText(testReference(Thread{ServerID: "target", Title: "登录页提示"}), "登录页提示")+" " || !strings.Contains(app.notice, "Referenced conversation \"登录页提示\"") {
		t.Fatalf("handoff did not start a new conversation with the reference: active=%d draft=%q notice=%q", c.active(), c.draft(), app.notice)
	}
}

func TestMobileComposerOffersGlobalConversationsAfterAt(t *testing.T) {
	m := testMobileApp()
	m.state.Projects[0].Threads = append(m.state.Projects[0].Threads, Thread{ID: 3, ServerID: "mobile-claude", Agent: agentClaude, Title: "登录修复"})
	m.state.Projects[1].Threads = []Thread{{ID: 4, ServerID: "mobile-codex", Agent: agentCodex, Title: "登录测试"}}
	th := m.state.Projects[0].Threads[0]
	th.HistoryLoaded = true
	m.openThread(th)
	m.showThread(th)
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	if err := tt.Click("Message"); err != nil {
		t.Fatal(err)
	}
	tt.Type("参考 @登")
	tt.Frame()
	if !tt.HasText("登录修复") || !tt.HasText("登录测试") || !tt.HasText("Codex · proj-b") || !tt.HasText("Referenced conversation") || tt.HasText("#1 thread-one") {
		t.Fatalf("phone @ menu missing global conversations or including the open conversation: %q", tt.Texts())
	}
	if err := tt.Click("登录测试"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m.draft != "参考 "+referenceText(testRef(agentCodex, "mobile-codex"), "登录测试")+" " || tt.HasText("Referenced conversation") {
		t.Fatalf("choosing a conversation drafted %q (menu still open: %v)", m.draft, tt.HasText("Referenced conversation"))
	}
}

func TestThreadRefsResolveInBackgroundProjects(t *testing.T) {
	c := testController()
	c.active, c.setActive = createSignal(1)
	c.cwd = "/open"
	src := handoffSource()
	src.ID = 7
	c.setThreads([]Thread{{ID: 1, Agent: agentCodex, Title: "Current: "}})
	c.projectCache = map[string][]Thread{"/open": c.threads(), "/other": {src}}
	out, sources, err := expandThreadRefs("参考 "+referenceText(testReference(src), src.Title), ThreadRef{}, c.resolveReference)
	if err != nil || len(sources) != 1 || !strings.Contains(out, "修复登录过期的问题") {
		t.Fatalf("background conversation not referenced: %q", out)
	}
	if status, _ := c.handoffState(7); status != subagent.Completed {
		t.Fatalf("background conversation's state not seen: %q", status)
	}
}
