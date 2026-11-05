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

func TestClaudeRunUsesTheChosenModelAndEffort(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := &testDispatch{}
	dir := t.TempDir()
	out := filepath.Join(dir, "argv")
	bin := filepath.Join(dir, "claude")
	// Publish complete argv atomically; the observer must not accept a partially
	// written file as the final invocation under a loaded full-suite run.
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$MIYA_OUT.tmp\"\nmv \"$MIYA_OUT.tmp\" \"$MIYA_OUT\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &chatController{cwd: dir, ctx: context.Background(), claudeBinary: bin, claudeEnv: []string{"MIYA_OUT=" + out}, run: d.push}
	c.threads, c.setThreads = createSignal([]Thread{})
	c.approvals, c.setApprovals = createSignal([]codex.Approval{})
	b := backendFor(agentClaude)
	b.SetModel(c, "opus")
	b.SetEffort(c, "high")
	if loadSetting("claudeModel") != "opus" || loadSetting("claudeEffort") != "high" {
		t.Fatal("choices not saved")
	}
	if c.submit(SendRequest{Agent: agentClaude, Text: "hi"}) == 0 {
		t.Fatal("not sent")
	}
	var args string
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		d.drain()
		if data, err := os.ReadFile(out); err == nil && len(data) > 0 {
			args = string(data)
			break
		}
	}
	if !strings.Contains(args, "--model\nopus\n") || !strings.Contains(args, "--effort\nhigh\n") {
		t.Fatalf("CLI arguments %q", args)
	}
	if got := b.ModelChoices(c); got[0] != "" || !strings.Contains(strings.Join(got, ","), "sonnet") {
		t.Fatalf("choices %q", got)
	}
}

func TestComposerPicksClaudeModel(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	c.claudeBinary = "/bin/true"
	c.setAgent(agentClaude)
	tt.Frame()
	if err := tt.Click("Default model"); err != nil {
		t.Fatalf("no model picker for Claude Code in %q: %v", tt.Texts(), err)
	}
	if err := tt.Click("Claude Sonnet 5.5"); err != nil {
		t.Fatal(err)
	}
	if c.claudeModel != "claude-sonnet-5-5" || !tt.HasText("Claude Sonnet 5.5") {
		t.Fatalf("model %q", c.claudeModel)
	}
	if err := tt.Click("Default effort"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("xhigh"); err != nil {
		t.Fatal(err)
	}
	if c.claudeEffort != "xhigh" {
		t.Fatalf("effort %q", c.claudeEffort)
	}
}

func TestClaudeDefaultModelFromInit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := testController()
	b := backendFor(agentClaude)
	if b.ModelLabel(c, "") != "Default model" {
		t.Fatal("label before any run")
	}
	c.noteClaudeModel("claude-opus-5-5")
	if got := b.ModelLabel(c, ""); got != "Default · Claude Opus 5.5" || loadSetting("claudeDefaultModel") != "claude-opus-5-5" {
		t.Fatalf("label %q", got)
	}
	if b.ModelLabel(c, "opus") != "Opus (latest)" || b.ModelLabel(c, "my-model") != "my-model" {
		t.Fatal("alias labels")
	}
}
