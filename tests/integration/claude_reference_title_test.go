package integration

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"miya/internal/appmodel"
	"miya/internal/claude/transcript"
	"miya/internal/model"
	"miya/internal/uiruntime"
)

func TestClaudeReferencePromptTitles(t *testing.T) {
	ref := testRef(appmodel.AgentClaude, "alma-design")
	mention := model.ReferenceText(ref, "本机中安装了 Alma 看看他有那些好的设计实现值得吸纳")
	other := model.ReferenceText(testRef(appmodel.AgentCodex, "drawer"), "抽屉 [布局] 调整")
	for _, tc := range []struct{ name, prompt, want string }{
		{"reference only", mention, "本机中安装了 Alma 看看他有那些好的设计实现值得吸纳"},
		{"request after reference", mention + "\n参考这个优化抽屉交互", "参考这个优化抽屉交互"},
		{"multiple leading references", "  " + mention + "，" + other + "\n比较两种抽屉布局", "比较两种抽屉布局"},
		{"reference labels only", mention + "\n" + other, transcript.ClippedTitle("本机中安装了 Alma 看看他有那些好的设计实现值得吸纳 抽屉 [布局] 调整")},
		{"inline reference", "检查 " + other + " 的动画", "检查 抽屉 [布局] 调整 的动画"},
		{"escaped title", other, "抽屉 [布局] 调整"},
		{"empty label", model.ReferenceText(ref, ""), "Referenced conversation"},
		{"command arguments", "<command-name>/review</command-name><command-args>" + other + " 的布局</command-args>", "/review 抽屉 [布局] 调整 的布局"},
		{"literal example", "`" + other + "`", transcript.ClippedTitle("`" + other + "`")},
		{"escaped example", `\` + other, transcript.ClippedTitle(`\` + other)},
		{"ordinary link", "看看 [参考](https://example.com)", "看看 [参考](https://example.com)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := transcript.PromptTitle(tc.prompt); got != tc.want {
				t.Fatalf("title=%q; want %q", got, tc.want)
			}
		})
	}
	snapshots, _ := json.Marshal([]model.ReferenceSnapshot{{Ref: ref, Title: "本机中安装了 Alma 看看他有那些好的设计实现值得吸纳", Context: "不应进入标题的上下文"}})
	if got := transcript.PromptTitle(mention + model.ContextEnvelopeOpen + string(snapshots) + model.ContextEnvelopeClose); got != "本机中安装了 Alma 看看他有那些好的设计实现值得吸纳" {
		t.Fatalf("context envelope entered the title: %q", got)
	}
}

func TestClaudeReferenceSessionTitlePriorityAndRefresh(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	uiruntime.InlineDispatch.Store(true)
	t.Cleanup(func() { uiruntime.InlineDispatch.Store(false) })
	c := testController()
	c.Cwd = t.TempDir()
	c.Active, c.SetActive = uiruntime.CreateSignal(0)
	mention := model.ReferenceText(testRef(appmodel.AgentClaude, "source"), strings.Repeat("很长的参考会话标题", 10))
	prompt := mention + "\n优化抽屉布局"
	entry, _ := json.Marshal(map[string]any{"type": "user", "cwd": c.Cwd, "message": map[string]string{"content": prompt}})
	path := writeClaudeSession(t, c.Cwd, "referenced", string(entry))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	at := info.ModTime().UnixMilli()
	c.SetThreads([]model.Thread{{ID: 1, Agent: appmodel.AgentClaude, ServerID: "referenced", Title: transcript.ClippedTitle(prompt), LastActivityAt: at}})
	c.LoadClaudeSessionsSync()
	thread := c.ThreadWithID(1)
	if thread.Title != "优化抽屉布局" || thread.LastActivityAt != at {
		t.Fatalf("cached title or activity incorrect: %+v", thread)
	}
	for _, tc := range []struct{ kind, field, value, want string }{
		{"summary", "summary", mention + " 参考设计整理", "参考设计整理"},
		{"ai-title", "aiTitle", mention + " 优化交互", "优化交互"},
		{"ai-title", "aiTitle", "生成的任务标题", "生成的任务标题"},
		{"custom-title", "customTitle", "手动标题 [保留]", "手动标题 [保留]"},
	} {
		line, _ := json.Marshal(map[string]string{"type": tc.kind, tc.field: tc.value})
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = file.Write(append(line, '\n'))
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		session, ok := transcript.Summarize(path)
		if !ok || session.Title != tc.want {
			t.Fatalf("%s title=%q; want %q", tc.kind, session.Title, tc.want)
		}
	}
}
