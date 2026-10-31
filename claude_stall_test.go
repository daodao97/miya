package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"miya/internal/claude"
)

func TestClaudeEmptyResultExplained(t *testing.T) {
	c := testController()
	c.claudeCommand = "claude --settings ~/proxy.json"
	c.setThreads([]Thread{{ID: 1, Agent: "claude-code", ServerID: "s1", Messages: []Message{{ID: 1, Role: "user", Content: "继续"}}}})
	var f claude.Frame
	_ = json.Unmarshal([]byte(`{"type":"result","is_error":true,"errors":["[ede_diagnostic] result_type=user last_content_type=n/a stop_reason=null"]}`), &f)
	c.handleClaude(1, f)
	msgs := c.threads()[0].Messages
	last := msgs[len(msgs)-1].Content
	if !strings.Contains(last, "没有收到模型回复") || !strings.Contains(last, "代理") || !strings.Contains(last, "stop_reason=null") {
		t.Fatalf("message %q", last)
	}
	// A real error after a reply keeps Claude Code's own words.
	if got := claudeErrorMessage("rate limited", true, true); got != "rate limited" {
		t.Fatalf("rewrote %q", got)
	}
	if got := claudeErrorMessage("x", false, false); strings.Contains(got, "自定义启动命令") {
		t.Fatalf("custom hint without a custom command: %q", got)
	}
}

func TestStalledHint(t *testing.T) {
	now := time.Now()
	base := Thread{Agent: "claude-code", Busy: true, LastOutputAtMs: now.Add(-90 * time.Second).UnixMilli()}
	if h := stalledHint(base, false, now); !strings.Contains(h, "1m 30s") {
		t.Fatalf("hint %q", h)
	}
	fresh := base
	fresh.LastOutputAtMs = now.Add(-10 * time.Second).UnixMilli()
	tool := base
	tool.Messages = []Message{{Role: "tool", Status: "inProgress"}}
	idle := base
	idle.Busy = false
	codex := base
	codex.Agent = ""
	for name, th := range map[string]Thread{"recent output": fresh, "tool running": tool, "not busy": idle, "codex": codex} {
		if h := stalledHint(th, false, now); h != "" {
			t.Errorf("%s: %q", name, h)
		}
	}
	if stalledHint(base, true, now) != "" {
		t.Error("hint while an approval waits")
	}
}

func TestStalledHintShowsInConversation(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	c.setThreads([]Thread{{ID: 1, Agent: "claude-code", ServerID: "s1", Title: "Slow", Busy: true, HistoryLoaded: true,
		StartedAtMs: time.Now().Add(-2 * time.Minute).UnixMilli(), LastOutputAtMs: time.Now().Add(-2 * time.Minute).UnixMilli(),
		Messages: []Message{{ID: 1, Role: "user", Content: "继续"}}}})
	c.setActive(1)
	tt.Frame()
	for _, s := range tt.Texts() {
		if strings.Contains(s, "没有收到回应") {
			return
		}
	}
	t.Fatalf("no stall hint in %q", tt.Texts())
}
