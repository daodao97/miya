//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaudePermissionModes(t *testing.T) {
	for policy, want := range map[string]string{"on-request": "manual", "on-failure": "auto", "never": "bypassPermissions", "": "manual"} {
		if got := claudePermissionMode(policy); got != want {
			t.Errorf("%q → %q, want %q", policy, got, want)
		}
	}
}

// The picker shows for Claude Code, and its choice reaches the CLI.
func TestClaudeRunUsesThePermissionPicker(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	dir := t.TempDir()
	out := filepath.Join(dir, "argv")
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$MIYA_OUT\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c.claudeBinary, c.claudeEnv = bin, []string{"MIYA_OUT=" + out}
	c.setAgent("claude-code")
	tt.Frame()
	if err := tt.Click("请求批准"); err != nil {
		t.Fatalf("no permission picker for Claude Code in %q: %v", tt.Texts(), err)
	}
	if !tt.HasText("Claude Code 应如何工作？") {
		t.Fatalf("picker title %q", tt.Texts())
	}
	if err := tt.Click("完全访问权限"); err != nil {
		t.Fatal(err)
	}
	// Keep the run's later updates queued: this test reads only what
	// sendClaude does on its own goroutine and what the CLI received.
	inlineDispatch.Store(false)
	t.Cleanup(func() { windowMu.Lock(); pending = nil; windowMu.Unlock() })
	c.rememberDraft("hello")
	c.send()
	if cur := c.current(); cur.Agent != "claude-code" || cur.PermissionPolicy != "never" {
		t.Fatalf("conversation %+v", cur)
	}
	var args []string
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if data, err := os.ReadFile(out); err == nil && len(data) > 0 {
			args = strings.Split(strings.TrimSpace(string(data)), "\n")
			break
		}
	}
	for i, a := range args {
		if a == "--permission-mode" && i+1 < len(args) {
			if args[i+1] != "bypassPermissions" {
				t.Fatalf("permission mode %q", args[i+1])
			}
			return
		}
	}
	t.Fatalf("no --permission-mode in %q", args)
}
