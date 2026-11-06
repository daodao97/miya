package integration

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	"miya/internal/composer"
	"miya/internal/controller"
	"miya/internal/model"
	"miya/internal/msgfmt"
	"miya/internal/subagent"
	"miya/internal/uiruntime"
)

func handoffSource() model.Thread {
	change := &codex.Item{}
	change.Changes = append(change.Changes, struct {
		Path string `json:"path"`
		Diff string `json:"diff"`
	}{Path: "auth/login.go", Diff: "+fix"})
	return model.Thread{ID: 2, ServerID: "source-session", Agent: appmodel.AgentClaude, Title: "登录修复", HistoryLoaded: true, Messages: []model.Message{
		{ID: 1, Role: "user", Content: "修复登录过期的问题"},
		{ID: 2, Role: "tool", Kind: "claudeTool", Content: "Edit\n{\"file_path\":\"auth/token.go\"}\nok", Status: "completed"},
		{ID: 3, Role: "tool", Kind: "fileChange", Item: change, Status: "completed"},
		{ID: 4, Role: "assistant", Content: "已修复：token 过期判断用错了时区。"},
		{ID: 5, Role: "tool", Kind: "claudeTool", Content: "Agent\n{}", Agents: []model.SubAgent{{ID: "t", Name: "跑测试", Status: subagent.Completed, Detail: "全部通过"}}},
	}}
}

func TestThreadRefsExpandIntoContextPackets(t *testing.T) {
	src := handoffSource()
	ref := testReference(src)
	resolve := func(r model.ThreadRef) (model.Thread, []model.Message, error) {
		if r == ref {
			return src, src.Messages, nil
		}
		return model.Thread{}, nil, fmt.Errorf("missing")
	}
	text := "参考 " + model.ReferenceText(ref, src.Title) + " 给登录页加上提示"
	if got := msgfmt.ThreadRefs(text); len(got) != 1 || got[0] != ref {
		t.Fatalf("bad refs: %v", got)
	}
	out, sources, err := controller.ExpandThreadRefs(text, model.ThreadRef{}, resolve)
	if err != nil || len(sources) != 1 {
		t.Fatalf("bad expansion: %v %v", sources, err)
	}
	for _, want := range []string{text, "source-session", "修复登录过期的问题", "已修复：token 过期判断用错了时区。", "auth/token.go", "auth/login.go", "跑测试 · Completed · 全部通过"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
	if again, more, err := controller.ExpandThreadRefs(out, model.ThreadRef{}, resolve); err != nil || again != out || len(more) != 0 {
		t.Fatal("snapshot was expanded again")
	}
	if got := appmodel.ThreadContextDisplay(out); strings.Contains(got, "miya-thread-context") || !strings.Contains(got, "[Referenced conversation: 登录修复]") {
		t.Fatal(got)
	}
	if got := msgfmt.ThreadRefTask(text); got != "参考 给登录页加上提示" {
		t.Fatal(got)
	}
}

func TestUnloadedThreadReferenceFailsWithoutSending(t *testing.T) {
	src := handoffSource()
	src.Messages = nil
	src.HistoryLoaded = false
	raw := model.ReferenceText(testReference(src), src.Title)
	got, sources, err := controller.ExpandThreadRefs(raw, model.ThreadRef{}, func(model.ThreadRef) (model.Thread, []model.Message, error) { return src, nil, nil })
	if err == nil || got != raw || len(sources) != 0 {
		t.Fatalf("unloaded reference accepted: %s %v", got, err)
	}
}

func TestThreadReferenceDoesNotChangeSourceConversation(t *testing.T) {
	for _, background := range []bool{false, true} {
		t.Run(fmt.Sprintf("background=%v", background), func(t *testing.T) {
			c := testController()
			c.Active, c.SetActive = uiruntime.CreateSignal(1)
			c.Agent, c.SetAgent = uiruntime.CreateSignal(appmodel.AgentClaude)
			c.Cwd = "/open"
			src := handoffSource()
			target := model.Thread{ID: 1, Agent: appmodel.AgentClaude, ServerID: "target", Title: "登录页", Busy: true, HistoryLoaded: true}
			if background {
				c.SetThreads([]model.Thread{target})
				c.ProjectCache = map[string][]model.Thread{"/other": {src}}
			} else {
				c.SetThreads([]model.Thread{target, src})
			}
			if id := c.Submit(model.SendRequest{ThreadID: 1, Text: "参考 " + model.ReferenceText(testReference(src), src.Title) + " 给登录页加上提示"}); id != 1 {
				t.Fatalf("submit returned %d", id)
			}
			target = c.ThreadWithID(1)
			if len(target.Queue) != 1 || !strings.Contains(target.Queue[0].Text, model.ContextEnvelopeOpen) {
				t.Fatalf("queued message not expanded: %+v", target.Queue)
			}
			assertUnchanged := func() {
				t.Helper()
				if got := c.ThreadWithID(src.ID); !reflect.DeepEqual(got, src) || len(c.Handoffs) != 0 {
					t.Fatalf("reference changed its source or created a handoff: %+v, %+v", got, c.Handoffs)
				}
			}
			assertUnchanged()
			c.Update(1, func(th *model.Thread) {
				th.Busy, th.Queue = false, nil
				th.Messages = append(th.Messages, model.Message{ID: c.NextID(), Role: "user", Content: "参考"}, model.Message{ID: c.NextID(), Role: "assistant", Content: "提示已加上"})
			})
			assertUnchanged()
		})
	}
}

func TestThreadChoiceInsertsReference(t *testing.T) {
	src := handoffSource()
	value := "看下 @登 然后修"
	token := composer.ComposerTokenAt(value, len("看下 @登"))
	want := "看下 " + model.ReferenceText(testReference(src), src.Title) + "  然后修"
	if got := composer.InsertThreadReference(value, token, testReference(src), src.Title); got != want {
		t.Fatal(got)
	}
}

func TestComposerOffersConversationsAfterAt(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	src := handoffSource()
	c.SetThreads([]model.Thread{{ID: 1, Agent: appmodel.AgentCodex, ServerID: "target", Title: "登录页提示", HistoryLoaded: true}, src})
	c.SetActive(1)
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
	if got := c.Draft(); got != "参考 "+model.ReferenceText(testReference(src), src.Title)+" " {
		t.Fatalf("choosing a conversation drafted %q", got)
	}
	app.HandoffToNewThread()
	if c.Active() != 0 || c.Draft() != model.ReferenceText(testReference(model.Thread{ServerID: "target", Title: "登录页提示"}), "登录页提示")+" " || !strings.Contains(app.Notice, "Referenced conversation \"登录页提示\"") {
		t.Fatalf("handoff did not start a new conversation with the reference: active=%d draft=%q notice=%q", c.Active(), c.Draft(), app.Notice)
	}
}

func TestMobileComposerOffersGlobalConversationsAfterAt(t *testing.T) {
	m := testMobileApp()
	m.State.Projects[0].Threads = append(m.State.Projects[0].Threads, model.Thread{ID: 3, ServerID: "mobile-claude", Agent: appmodel.AgentClaude, Title: "登录修复"})
	m.State.Projects[1].Threads = []model.Thread{{ID: 4, ServerID: "mobile-codex", Agent: appmodel.AgentCodex, Title: "登录测试"}}
	th := m.State.Projects[0].Threads[0]
	th.HistoryLoaded = true
	m.OpenThread(th)
	m.ShowThread(th)
	tt := ui.NewTester(m.ViewRoot, 390, 750)
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
	if m.Draft != "参考 "+model.ReferenceText(testRef(appmodel.AgentCodex, "mobile-codex"), "登录测试")+" " || tt.HasText("Referenced conversation") {
		t.Fatalf("choosing a conversation drafted %q (menu still open: %v)", m.Draft, tt.HasText("Referenced conversation"))
	}
}

func TestThreadRefsResolveInBackgroundProjects(t *testing.T) {
	c := testController()
	c.Active, c.SetActive = uiruntime.CreateSignal(1)
	c.Cwd = "/open"
	src := handoffSource()
	src.ID = 7
	c.SetThreads([]model.Thread{{ID: 1, Agent: appmodel.AgentCodex, Title: "Current: "}})
	c.ProjectCache = map[string][]model.Thread{"/open": c.Threads(), "/other": {src}}
	out, sources, err := controller.ExpandThreadRefs("参考 "+model.ReferenceText(testReference(src), src.Title), model.ThreadRef{}, c.ResolveReference)
	if err != nil || len(sources) != 1 || !strings.Contains(out, "修复登录过期的问题") {
		t.Fatalf("background conversation not referenced: %q", out)
	}
	if status, _ := c.HandoffState(7); status != subagent.Completed {
		t.Fatalf("background conversation's state not seen: %q", status)
	}
}
