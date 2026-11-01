package main

import "testing"

func TestAddThreadToBackgroundProject(t *testing.T) {
	c := testController()
	c.cwd = "/open"
	c.setThreads([]Thread{{ID: 1, Title: "open"}})
	c.addThread("/other", Thread{ID: 2, Title: "background"})
	c.addThread("", Thread{ID: 3, Title: "here"})
	if got := c.threadWorkspace(2); got != "/other" {
		t.Fatalf("background conversation is in %q", got)
	}
	if c.threadWithID(2).Title != "background" {
		t.Fatal("background conversation not found")
	}
	for _, th := range c.threads() {
		if th.ID == 2 {
			t.Fatal("background conversation shown in the open project")
		}
	}
	if c.threadWorkspace(3) != "/open" || c.threads()[0].ID != 3 {
		t.Fatal("open-project conversation misplaced")
	}
	c.update(2, func(th *Thread) { th.Busy = true })
	if !c.threadWithID(2).Busy {
		t.Fatal("background conversation not updated")
	}
}

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
