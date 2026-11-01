package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"miya/internal/codex"
)

func handoffSource() Thread {
	change := &codex.Item{}
	change.Changes = append(change.Changes, struct {
		Path string `json:"path"`
		Diff string `json:"diff"`
	}{Path: "auth/login.go", Diff: "+fix"})
	return Thread{ID: 2, Agent: agentClaude, Title: "登录修复", HistoryLoaded: true, Messages: []Message{
		{ID: 1, Role: "user", Content: "修复登录过期的问题"},
		{ID: 2, Role: "tool", Kind: "claudeTool", Content: "Edit\n{\"file_path\":\"auth/token.go\"}\nok", Status: "completed"},
		{ID: 3, Role: "tool", Kind: "fileChange", Item: change, Status: "completed"},
		{ID: 4, Role: "assistant", Content: "已修复：token 过期判断用错了时区。"},
		{ID: 5, Role: "tool", Kind: "claudeTool", Content: "Agent\n{}", Agents: []SubAgent{{ID: "t", Name: "跑测试", Status: subAgentCompleted, Detail: "全部通过"}}},
	}}
}

func TestThreadRefsExpandIntoContextPackets(t *testing.T) {
	src := handoffSource()
	resolve := func(id int) (Thread, []Message, bool) {
		if id == src.ID {
			return src, src.Messages, true
		}
		return Thread{}, nil, false
	}
	text := "参考 @#2 给登录页加上提示，@#9 不存在，@#1 是自己"
	if got := threadRefs(text); len(got) != 3 || got[0] != 2 {
		t.Fatalf("bad refs: %v", got)
	}
	out, sources := expandThreadRefs(text, 1, resolve)
	if len(sources) != 1 || sources[0].ID != 2 {
		t.Fatalf("bad sources: %+v", sources)
	}
	for _, want := range []string{"参考 「对话：登录修复」 给登录页加上提示", "@#9", "@#1", "<miya-thread-context id=\"2\"", "修复登录过期的问题", "已修复：token 过期判断用错了时区。", "auth/token.go", "auth/login.go", "跑测试 · 已完成 · 全部通过", "</miya-thread-context>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expanded message lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\nok") {
		t.Fatalf("tool output leaked into the packet:\n%s", out)
	}
	if again, more := expandThreadRefs(out, 1, resolve); again != out || len(more) != 0 {
		t.Fatal("an expanded message expanded again")
	}
	if got := threadContextDisplay(out); strings.Contains(got, "<miya-thread-context") || !strings.Contains(got, "〔引用对话：登录修复〕") {
		t.Fatalf("bad display: %q", got)
	}
	if got := threadRefTask(text); got != "参考 给登录页加上提示， 不存在， 是自己" {
		t.Fatalf("bad task: %q", got)
	}
}

func TestUnloadedThreadPacketSaysSo(t *testing.T) {
	p := threadPacket(Thread{ID: 3, Agent: agentCodex, Title: `a "quoted" <title>`}, nil)
	if !strings.Contains(p, "还没有加载") || !strings.Contains(threadContextDisplay(p), `〔引用对话：a "quoted" <title>〕`) {
		t.Fatalf("bad unloaded packet: %s", p)
	}
}

func TestHandoffFollowsTheTargetConversation(t *testing.T) {
	c := testController()
	c.active, c.setActive = createSignal(1)
	c.agent, c.setAgent = createSignal("claude-code")
	src := handoffSource()
	c.setThreads([]Thread{{ID: 1, Agent: agentClaude, ServerID: "target", Title: "登录页", Busy: true, HistoryLoaded: true}, src})
	if id := c.submit(sendRequest{ThreadID: 1, Text: "参考 @#2 给登录页加上提示"}); id != 1 {
		t.Fatalf("submit returned %d", id)
	}
	var target, source Thread
	for _, th := range c.threads() {
		switch th.ID {
		case 1:
			target = th
		case 2:
			source = th
		}
	}
	if len(target.Queue) != 1 || !strings.Contains(target.Queue[0].Text, "<miya-thread-context id=\"2\"") {
		t.Fatalf("queued message not expanded: %+v", target.Queue)
	}
	last := source.Messages[len(source.Messages)-1]
	if last.Kind != "handoff" || len(last.Agents) != 1 || last.Agents[0].Status != subAgentRunning || last.Agents[0].Name != "参考 给登录页加上提示" || last.Content != "" {
		t.Fatalf("handoff not followed: %+v", last)
	}
	if activityTitle(last) != "已交付到对话 #1" {
		t.Fatalf("bad title: %q", activityTitle(last))
	}
	c.update(1, func(th *Thread) {
		th.Busy, th.Queue = false, nil
		th.Messages = append(th.Messages, Message{ID: c.nextID(), Role: "user", Content: "参考"}, Message{ID: c.nextID(), Role: "assistant", Content: "提示已加上\n细节"})
	})
	for _, th := range c.threads() {
		if th.ID == 2 {
			a := th.Messages[len(th.Messages)-1].Agents[0]
			if a.Status != subAgentCompleted || a.Detail != "提示已加上" {
				t.Fatalf("handoff state not followed: %+v", a)
			}
		}
	}
}

func TestThreadChoiceInsertsReference(t *testing.T) {
	id, ok := threadChoiceID("#12 登录修复")
	if !ok || id != 12 {
		t.Fatalf("bad choice id: %d %v", id, ok)
	}
	if _, ok := threadChoiceID("src/main.go"); ok {
		t.Fatal("a file was taken for a conversation")
	}
	value := "看下 @登 然后修"
	token := composerTokenAt(value, len("看下 @登"))
	if got := insertThreadReference(value, token, 12); got != "看下 @#12  然后修" {
		t.Fatalf("bad insertion: %q", got)
	}
}

func TestThreadRefsAfterPunctuationNotInWords(t *testing.T) {
	if got := threadRefs("看下，@#3。还有（@#4）和中文@#5"); len(got) != 3 || got[0] != 3 || got[2] != 5 {
		t.Fatalf("refs after punctuation missed: %v", got)
	}
	if got := threadRefs("mail a@#6 or x#@#7 or @#8x"); len(got) != 0 {
		t.Fatalf("refs taken from words: %v", got)
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
	if !tt.HasText("#2 登录修复") || !tt.HasText("对话与项目文件") || !tt.HasText("对话 · Claude Code · demo") || tt.HasText("#1 登录页提示") {
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
	if got := c.draft(); got != "参考 @#2 " {
		t.Fatalf("choosing a conversation drafted %q", got)
	}
	app.handoffToNewThread()
	if c.active() != 0 || c.draft() != "@#1 " || !strings.Contains(app.notice, "已引用对话 #1") {
		t.Fatalf("handoff did not start a new conversation with the reference: active=%d draft=%q notice=%q", c.active(), c.draft(), app.notice)
	}
}

func TestMobileComposerOffersGlobalConversationsAfterAt(t *testing.T) {
	m := testMobileApp()
	m.state.Projects[0].Threads = append(m.state.Projects[0].Threads, Thread{ID: 3, Agent: agentClaude, Title: "登录修复"})
	m.state.Projects[1].Threads = []Thread{{ID: 4, Agent: agentCodex, Title: "登录测试"}}
	th := m.state.Projects[0].Threads[0]
	th.HistoryLoaded = true
	m.openThread(th)
	m.showThread(th)
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Type("参考 @登")
	tt.Frame()
	if !tt.HasText("#3 登录修复") || !tt.HasText("#4 登录测试") || !tt.HasText("Codex · proj-b") || !tt.HasText("引用对话") || tt.HasText("#1 thread-one") {
		t.Fatalf("phone @ menu missing global conversations or including the open conversation: %q", tt.Texts())
	}
	if err := tt.Click("登录测试"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m.draft != "参考 @#4 " || tt.HasText("引用对话") {
		t.Fatalf("choosing a conversation drafted %q (menu still open: %v)", m.draft, tt.HasText("引用对话"))
	}
}

func TestThreadRefsResolveInBackgroundProjects(t *testing.T) {
	c := testController()
	c.active, c.setActive = createSignal(1)
	c.cwd = "/open"
	src := handoffSource()
	src.ID = 7
	c.setThreads([]Thread{{ID: 1, Agent: agentCodex, Title: "当前"}})
	c.projectCache = map[string][]Thread{"/open": c.threads(), "/other": {src}}
	out, sources := expandThreadRefs("参考 @#7", 1, c.resolveThreadRef)
	if len(sources) != 1 || !strings.Contains(out, "修复登录过期的问题") {
		t.Fatalf("background conversation not referenced: %q", out)
	}
	if status, _ := c.handoffState(7); status != subAgentCompleted {
		t.Fatalf("background conversation's state not seen: %q", status)
	}
}
