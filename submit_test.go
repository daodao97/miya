//go:build !windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"miya/internal/codex"
)

// submit needs no window: no active conversation, draft or attachments.
func TestSubmitWithoutWindowState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	inlineDispatch.Store(false)
	t.Cleanup(func() { windowMu.Lock(); pending = nil; windowMu.Unlock() })
	dir := t.TempDir()
	out := filepath.Join(dir, "argv")
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$MIYA_OUT\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &chatController{cwd: dir, ctx: context.Background(), claudeBinary: bin, claudeEnv: []string{"MIYA_OUT=" + out}}
	c.threads, c.setThreads = createSignal([]Thread{})
	c.approvals, c.setApprovals = createSignal([]codex.Approval{})
	if c.submit(sendRequest{Agent: agentClaude, Text: "   "}) != 0 {
		t.Fatal("an empty message was sent")
	}
	id := c.submit(sendRequest{Agent: agentClaude, Text: "hello from the server", Policy: "never"})
	th := c.threadWithID(id)
	if id == 0 || th.Agent != agentClaude || !th.Busy || th.PermissionPolicy != "never" || len(th.Messages) != 1 || th.Messages[0].Content != "hello from the server" {
		t.Fatalf("conversation %+v", th)
	}
	if c.submit(sendRequest{ThreadID: id, Text: "again"}) != id || len(c.threadWithID(id).Queue) != 1 || len(c.threadWithID(id).Messages) != 1 {
		t.Fatal("a message for a busy conversation was not queued")
	}
	var args string
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if data, err := os.ReadFile(out); err == nil && len(data) > 0 {
			args = string(data)
			break
		}
	}
	if !strings.Contains(args, "--session-id\n"+th.ServerID) || !strings.Contains(args, "bypassPermissions") {
		t.Fatalf("CLI arguments %q", args)
	}
	for deadline := time.Now().Add(time.Minute); pendingCount() < 2 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
	}
}
