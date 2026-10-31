package main

import (
	"testing"
)

func TestCachedThreadIDsDoNotCollideWithNewThreads(t *testing.T) {
	c := &chatController{projectCache: map[string][]Thread{
		"/first":  {{ID: 1, ServerID: "a"}, {ID: 1, ServerID: "b"}},
		"/second": {{ID: 1, ServerID: "c"}, {ID: 0, ServerID: "d"}},
	}}
	c.assignCachedThreadIDs()
	seen := map[int]bool{}
	for _, threads := range c.projectCache {
		for _, thread := range threads {
			if thread.ID <= 0 || seen[thread.ID] {
				t.Fatalf("duplicate or invalid restored ID: %d", thread.ID)
			}
			seen[thread.ID] = true
		}
	}
	newID := c.nextID()
	if seen[newID] {
		t.Fatalf("new thread reused cached ID: %d", newID)
	}
}

func TestCachedThreadIDsSurviveDesktopRestart(t *testing.T) {
	c := &chatController{projectCache: map[string][]Thread{
		"/a": {{ID: 345, ServerID: "old"}, {ID: 8, ServerID: "another"}},
		"/b": {{ID: 900, ServerID: "other-project"}},
	}}
	c.assignCachedThreadIDs()
	if c.projectCache["/a"][0].ID != 345 || c.projectCache["/a"][1].ID != 8 || c.projectCache["/b"][0].ID != 900 {
		t.Fatalf("restart renumbered a published conversation: %+v", c.projectCache)
	}
	if got := c.nextID(); got <= 900 {
		t.Fatalf("new thread reused a published ID: %d", got)
	}
}
