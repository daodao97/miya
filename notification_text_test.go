package main

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"miya/internal/claude"
	"miya/internal/codex"
)

func TestNoticeSummary(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"\n# 完成\n\n- **连接已修复。** 测试通过。", "连接已修复。"},
		{"```sh\ngo test ./...\n```\nFixed the buttons. Tests passed.", "Fixed the buttons."},
		{"# 标题\n~~~go\ncode\n~~~", ""},
		{"已更新 [通知](https://example.com) 和 `API`。后续。", "已更新 通知 和 API。"},
		{"结果\u202e\x00已保存。", "结果 已保存。"},
		{`{"summary":"按钮已修复。测试通过。"}`, "按钮已修复。"},
		{"```json\n{\"summary\":\"按钮已修复。\"}\n```", "按钮已修复。"},
		{`{"summary":42}`, ""},
	} {
		if got := noticeSummary(tc.in); got != tc.want {
			t.Errorf("%q => %q, want %q", tc.in, got, tc.want)
		}
	}
	got := noticeSummary(strings.Repeat("中", 300))
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) != 160 || !strings.HasSuffix(got, "…") {
		t.Fatal("summary is not bounded Unicode text", got)
	}
}

func TestCompletionNoticeUsesOnlyCurrentFinalReply(t *testing.T) {
	old := Thread{ID: 1, Busy: true, TurnID: "current"}
	next := Thread{ID: 1, CompletedAtMs: 200, Title: "旧会话标题", StartedAtMs: 100, Messages: []Message{
		{Role: "assistant", TurnID: "old", Content: "上一轮完成。"},
		{Role: "user", Content: "修复手机按钮"},
		{Role: "activity", TurnID: "current", Content: "正在思考。"},
		{Role: "tool", TurnID: "current", Content: "工具输出。"},
		{Role: "assistant", TurnID: "current", Content: "我来检查。", Item: &codex.Item{Phase: "commentary"}},
		{Role: "assistant", TurnID: "foreign", Content: "其他轮结果。"},
	}}
	if got := completionNoticeBody(old, next); got != "Task: 修复手机按钮" {
		t.Fatal("used progress or an unrelated reply", got)
	}
	next.Messages = append(next.Messages, Message{Role: "assistant", TurnID: "current", Content: "# 完成\n按钮已修复。测试通过。", Item: &codex.Item{Phase: "final_answer"}})
	if n, ok := completionNotice(old, next); !ok || n.Body != "按钮已修复。" || n.Title != "Miya · Completed" {
		t.Fatal("final answer not used", n)
	}
	next.Status = "Failed"
	next.Messages = append(next.Messages, Message{Role: "error", TurnID: "current", Content: "连接已断开。请重试。"})
	if n, _ := completionNotice(old, next); n.Body != "连接已断开。" || n.Title != "Miya · Failed" {
		t.Fatal("failure showed a successful answer", n)
	}
}

func TestCompletionNoticeClaudeTurnBoundaryAndTaskFallback(t *testing.T) {
	old := Thread{Busy: true}
	next := Thread{StartedAtMs: 100, Messages: []Message{
		{Role: "user", Content: "修复连接"},
		{Role: "assistant", Content: "旧结果。", StartedAtMs: 50},
		{Role: "user", Content: "继续吧"},
	}}
	if got := completionNoticeBody(old, next); got != "Task: 修复连接" {
		t.Fatal("reused an old result or continuation", got)
	}
	next.Messages = append(next.Messages, Message{Role: "assistant", Content: "新结果。", StartedAtMs: 100})
	if got := completionNoticeBody(old, next); got != "新结果。" {
		t.Fatal(got)
	}
	// An autonomous turn need not append a new user message.
	next.StartedAtMs = 200
	if got := completionNoticeBody(old, next); got != "Task: 修复连接" {
		t.Fatal("autonomous turn reused the previous answer", got)
	}
	for _, title := range []string{"", "Codex", "/Users/private/project", "~/work/project", `C:\work\project`} {
		if got := completionNoticeBody(old, Thread{Title: title}); got != "Task completed. Open the conversation to view the result." {
			t.Fatal("invalid task fallback", got)
		}
	}
}

func TestApprovalNoticeDescribesRequest(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		a          codex.Approval
	}{
		{"question", "采用哪种方案？", codex.Approval{Method: "item/tool/requestUserInput", Questions: []codex.Question{{Question: "采用哪种方案？", IsSecret: true}}}},
		{"command", "Allow execution of go test ./...?", codex.Approval{Method: "item/commandExecution/requestApproval", Command: "go test ./..."}},
		{"reason", "需要访问测试服务器", codex.Approval{Method: "item/permissions/requestApproval", Reason: "需要访问测试服务器"}},
		{"files", "Confirm the file changes to continue the task.", codex.Approval{Method: "item/fileChange/requestApproval"}},
		{"MCP", "允许操作浏览器吗？", codex.Approval{Method: "mcpServer/elicitation/request", Message: "允许操作浏览器吗？"}},
		{"Claude Bash", "Allow execution of go test ./...?", codex.Approval{Method: "claude/permission", Command: "Bash", Reason: `{"command":"go test ./..."}`, Payload: json.RawMessage(`{"command":"go test ./..."}`)}},
		{"Claude Edit", "Permission is required to use Edit to continue the task.", codex.Approval{Method: "claude/permission", Command: "Edit", Reason: `{"file_path":"private"}`}},
		{"fallback", "Task: 通知文案", codex.Approval{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := approvalNotice(Thread{ID: 7, Title: "通知文案"}, tc.a)
			if n.Body != tc.body || n.Thread != "7" {
				t.Fatal(n)
			}
			if tc.name == "question" && n.Title != "Miya · Answer needed" {
				t.Fatal(n)
			}
		})
	}
}

func TestClaudeLiveReplyProvidesNotificationExcerpt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := testController()
	c.update(1, func(t *Thread) { t.Agent = agentClaude; t.StartedAtMs = 1 })
	var f claude.Frame
	_ = json.Unmarshal([]byte(`{"type":"assistant","message":{"id":"final","content":[{"type":"text","text":"按钮已修复。测试通过。"}]}}`), &f)
	c.handleClaude(1, f)
	_ = json.Unmarshal([]byte(`{"type":"result","keepingAlive":true,"is_error":false}`), &f)
	// KeepingAlive is local lifecycle metadata, not part of stream-json.
	f.KeepingAlive = true
	c.handleClaude(1, f)
	thread := c.threads()[0]
	if thread.Busy || thread.CompletionNoticeBody != "按钮已修复。" {
		t.Fatalf("missing live Claude result: %+v", thread)
	}
	c.update(1, func(t *Thread) { t.Busy = true })
	if c.threads()[0].CompletionNoticeBody != "" {
		t.Fatal("a new turn retained the previous notification")
	}
}
