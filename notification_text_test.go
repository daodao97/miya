package main

import (
	json "encoding/json"
	appmodel "miya/internal/appmodel"
	claude "miya/internal/claude"
	testing "testing"
)

func TestClaudeLiveReplyProvidesNotificationExcerpt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := testController()
	c.Update(1, func(t *appmodel.Thread) { t.Agent = appmodel.AgentClaude; t.StartedAtMs = 1 })
	var f claude.Frame
	_ = json.Unmarshal([]byte(`{"type":"assistant","message":{"id":"final","content":[{"type":"text","text":"按钮已修复。测试通过。"}]}}`), &f)
	c.HandleClaude(1, f)
	_ = json.Unmarshal([]byte(`{"type":"result","keepingAlive":true,"is_error":false}`), &f)
	// KeepingAlive is local lifecycle metadata, not part of stream-json.
	f.KeepingAlive = true
	c.HandleClaude(1, f)
	thread := c.Threads()[0]
	if thread.Busy || thread.CompletionNoticeBody != "按钮已修复。" {
		t.Fatalf("missing live Claude result: %+v", thread)
	}
	c.Update(1, func(t *appmodel.Thread) { t.Busy = true })
	if c.Threads()[0].CompletionNoticeBody != "" {
		t.Fatal("a new turn retained the previous notification")
	}
}
