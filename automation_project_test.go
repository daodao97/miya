package main

import (
	appmodel "miya/internal/appmodel"
	testing "testing"
)

func TestAddThreadToBackgroundProject(t *testing.T) {
	c := testController()
	c.Cwd = "/open"
	c.SetThreads([]appmodel.Thread{{ID: 1, Title: "open"}})
	c.AddThread("/other", appmodel.Thread{ID: 2, Title: "background"})
	c.AddThread("", appmodel.Thread{ID: 3, Title: "here"})
	if got := c.ThreadWorkspace(2); got != "/other" {
		t.Fatalf("background conversation is in %q", got)
	}
	if c.ThreadWithID(2).Title != "background" {
		t.Fatal("background conversation not found")
	}
	for _, th := range c.Threads() {
		if th.ID == 2 {
			t.Fatal("background conversation shown in the open project")
		}
	}
	if c.ThreadWorkspace(3) != "/open" || c.Threads()[0].ID != 3 {
		t.Fatal("open-project conversation misplaced")
	}
	c.Update(2, func(th *appmodel.Thread) { th.Busy = true })
	if !c.ThreadWithID(2).Busy {
		t.Fatal("background conversation not updated")
	}
}
