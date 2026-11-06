//go:build !windows

package main

import (
	appmodel "miya/internal/appmodel"
	controller "miya/internal/controller"
	testing "testing"
)

// The Codex fixture is a Python script, which Windows cannot run as a binary.
func TestWorkspaceSwitchKeepsRunningClaudeConversations(t *testing.T) {
	c, _, _ := codexResumeFixture(t)
	first, second := c.Cwd, t.TempDir()
	c.AddThread("", appmodel.Thread{ID: 901, Agent: appmodel.AgentClaude, ServerID: "s-busy", Title: "running", Busy: true, Loaded: true, HistoryLoaded: true})
	c.AddThread("", appmodel.Thread{ID: 902, Agent: appmodel.AgentClaude, ServerID: "s-idle", Title: "idle", Loaded: true, HistoryLoaded: true})
	if err := c.ChangeWorkspace(second); err != nil {
		t.Fatal(err)
	}
	running := c.ThreadWithID(901)
	if !running.Busy || !running.Loaded || c.ThreadWorkspace(901) != first {
		t.Fatalf("switching project stopped a running Claude conversation: %+v in %q", running, c.ThreadWorkspace(901))
	}
	if c.ThreadWithID(902).Loaded {
		t.Fatal("idle Claude conversation was kept loaded")
	}
	if !(&controller.Service{C: c}).Busy() {
		t.Fatal("the background run does not keep the service up")
	}
	c.FinishClaude(901, first, nil)
	if done := c.ThreadWithID(901); done.Busy || done.CompletedAtMs == 0 {
		t.Fatalf("background run did not finish: %+v", done)
	}
	if err := c.ChangeWorkspace(first); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, th := range c.Threads() {
		if th.ID == 901 {
			found = true
		}
	}
	if !found {
		t.Fatal("conversation lost after switching back")
	}
}
