package main

import (
	"encoding/json"
	"testing"

	"miya/internal/claude"
)

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
