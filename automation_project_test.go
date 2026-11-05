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
