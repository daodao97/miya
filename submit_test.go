//go:build !windows

package main

import (
	context "context"
	appmodel "miya/internal/appmodel"
	codex "miya/internal/codex"
	controller "miya/internal/controller"
	uiruntime "miya/internal/uiruntime"
	os "os"
	filepath "path/filepath"
	strings "strings"
	testing "testing"
	time "time"
)

// submit needs no window: no active conversation, draft or attachments.
func TestSubmitWithoutWindowState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	uiruntime.InlineDispatch.Store(false)
	t.Cleanup(func() { uiruntime.WindowMu.Lock(); uiruntime.Pending = nil; uiruntime.WindowMu.Unlock() })
	dir := t.TempDir()
	out := filepath.Join(dir, "argv")
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$MIYA_OUT\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &controller.Controller{WindowState: appmodel.WindowState{Cwd: dir, Ctx: context.Background()}, ClaudeBinary: bin, ClaudeEnv: []string{"MIYA_OUT=" + out}}
	c.Threads, c.SetThreads = uiruntime.CreateSignal([]appmodel.Thread{})
	c.Approvals, c.SetApprovals = uiruntime.CreateSignal([]codex.Approval{})
	if c.Submit(appmodel.SendRequest{Agent: appmodel.AgentClaude, Text: "   "}) != 0 {
		t.Fatal("an empty message was sent")
	}
	id := c.Submit(appmodel.SendRequest{Agent: appmodel.AgentClaude, Text: "hello from the server", Policy: "never"})
	th := c.ThreadWithID(id)
	if id == 0 || th.Agent != appmodel.AgentClaude || !th.Busy || th.PermissionPolicy != "never" || len(th.Messages) != 1 || th.Messages[0].Content != "hello from the server" {
		t.Fatalf("conversation %+v", th)
	}
	if c.Submit(appmodel.SendRequest{ThreadID: id, Text: "again"}) != id || len(c.ThreadWithID(id).Queue) != 1 || len(c.ThreadWithID(id).Messages) != 1 {
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
