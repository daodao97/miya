//go:build !windows

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"miya/internal/appmodel"
	"miya/internal/codex"
	"miya/internal/controller"
	"miya/internal/model"
	"miya/internal/prefs"
	"miya/internal/uiruntime"
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
	c := &controller.Controller{WindowState: appmodel.WindowState{Cwd: dir, Ctx: context.Background()}, ClaudeBinary: bin, ClaudeEnv: []string{"MIYA_OUT=" + out}, Run: d.push}
	c.Threads, c.SetThreads = uiruntime.CreateSignal([]model.Thread{})
	c.Approvals, c.SetApprovals = uiruntime.CreateSignal([]codex.Approval{})
	b := c.BackendFor(appmodel.AgentClaude)
	b.SetModel("opus")
	b.SetEffort("high")
	if prefs.Load("claudeModel") != "opus" || prefs.Load("claudeEffort") != "high" {
		t.Fatal("choices not saved")
	}
	if c.Submit(model.SendRequest{Agent: appmodel.AgentClaude, Text: "hi"}) == 0 {
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
	if got := b.ModelChoices(); got[0] != "" || !strings.Contains(strings.Join(got, ","), "sonnet") {
		t.Fatalf("choices %q", got)
	}
}

func TestComposerPicksClaudeModel(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.ClaudeBinary = "/bin/true"
	c.SetAgent(appmodel.AgentClaude)
	tt.Frame()
	if err := tt.Click("Default model"); err != nil {
		t.Fatalf("no model picker for Claude Code in %q: %v", tt.Texts(), err)
	}
	if err := tt.Click("Claude Sonnet 5.5"); err != nil {
		t.Fatal(err)
	}
	if c.ClaudeModel != "claude-sonnet-5-5" || !tt.HasText("Claude Sonnet 5.5") {
		t.Fatalf("model %q", c.ClaudeModel)
	}
	if err := tt.Click("Default effort"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("xhigh"); err != nil {
		t.Fatal(err)
	}
	if c.ClaudeEffort != "xhigh" {
		t.Fatalf("effort %q", c.ClaudeEffort)
	}
}

func TestClaudeDefaultModelFromInit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := testController()
	b := c.BackendFor(appmodel.AgentClaude)
	if b.ModelLabel("") != "Default model" {
		t.Fatal("label before any run")
	}
	c.NoteClaudeModel("claude-opus-5-5")
	if got := b.ModelLabel(""); got != "Default · Claude Opus 5.5" || prefs.Load("claudeDefaultModel") != "claude-opus-5-5" {
		t.Fatalf("label %q", got)
	}
	if b.ModelLabel("opus") != "Opus (latest)" || b.ModelLabel("my-model") != "my-model" {
		t.Fatal("alias labels")
	}
}
