package integration

import (
	"testing"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/controller"
)

func TestCachedThreadIDsDoNotCollideWithNewThreads(t *testing.T) {
	c := &controller.Controller{WindowState: appmodel.WindowState{ProjectCache: map[string][]model.Thread{
		"/first":  {{ID: 1, ServerID: "a"}, {ID: 1, ServerID: "b"}},
		"/second": {{ID: 1, ServerID: "c"}, {ID: 0, ServerID: "d"}},
	}}}
	c.AssignCachedThreadIDs()
	seen := map[int]bool{}
	for _, threads := range c.ProjectCache {
		for _, thread := range threads {
			if thread.ID <= 0 || seen[thread.ID] {
				t.Fatalf("duplicate or invalid restored ID: %d", thread.ID)
			}
			seen[thread.ID] = true
		}
	}
	newID := c.NextID()
	if seen[newID] {
		t.Fatalf("new thread reused cached ID: %d", newID)
	}
}

func TestCachedThreadIDsSurviveDesktopRestart(t *testing.T) {
	c := &controller.Controller{WindowState: appmodel.WindowState{ProjectCache: map[string][]model.Thread{
		"/a": {{ID: 345, ServerID: "old"}, {ID: 8, ServerID: "another"}},
		"/b": {{ID: 900, ServerID: "other-project"}},
	}}}
	c.AssignCachedThreadIDs()
	if c.ProjectCache["/a"][0].ID != 345 || c.ProjectCache["/a"][1].ID != 8 || c.ProjectCache["/b"][0].ID != 900 {
		t.Fatalf("restart renumbered a published conversation: %+v", c.ProjectCache)
	}
	if got := c.NextID(); got <= 900 {
		t.Fatalf("new thread reused a published ID: %d", got)
	}
}
