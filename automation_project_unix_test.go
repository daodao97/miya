//go:build !windows

package main

import (
	"testing"
)

// The Codex fixture is a Python script, which Windows cannot run as a binary.
func TestWorkspaceSwitchKeepsRunningClaudeConversations(t *testing.T) {
	c, _, _ := codexResumeFixture(t)
	first, second := c.cwd, t.TempDir()
	c.addThread("", Thread{ID: 901, Agent: agentClaude, ServerID: "s-busy", Title: "running", Busy: true, Loaded: true, HistoryLoaded: true})
	c.addThread("", Thread{ID: 902, Agent: agentClaude, ServerID: "s-idle", Title: "idle", Loaded: true, HistoryLoaded: true})
	if err := c.changeWorkspace(second); err != nil {
		t.Fatal(err)
	}
	running := c.threadWithID(901)
	if !running.Busy || !running.Loaded || c.threadWorkspace(901) != first {
		t.Fatalf("switching project stopped a running Claude conversation: %+v in %q", running, c.threadWorkspace(901))
	}
	if c.threadWithID(902).Loaded {
		t.Fatal("idle Claude conversation was kept loaded")
	}
	if !(&service{c: c}).busy() {
		t.Fatal("the background run does not keep the service up")
	}
	c.finishClaude(901, first, nil)
	if done := c.threadWithID(901); done.Busy || done.CompletedAtMs == 0 {
		t.Fatalf("background run did not finish: %+v", done)
	}
	if err := c.changeWorkspace(first); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, th := range c.threads() {
		if th.ID == 901 {
			found = true
		}
	}
	if !found {
		t.Fatal("conversation lost after switching back")
	}
}
