//go:build !mygo_ui

package integration

import (
	threadlist "miya/internal/chat/history"
	"miya/internal/chat/model"
	projectmodel "miya/internal/project/model"
	"path/filepath"
	"testing"
)

func TestNSGoSidebarDeletesConversationInItsProject(t *testing.T) {
	for _, background := range []bool{false, true} {
		name := "current"
		if background {
			name = "background"
		}
		t.Run(name, func(t *testing.T) {
			c := isolatedController(t)
			other := filepath.Join(t.TempDir(), "other")
			target := model.Thread{ID: 1, ServerID: "target", Title: "Delete this conversation", Busy: true}
			keep := model.Thread{ID: 2, ServerID: "keep", Title: "Keep this conversation", HistoryLoaded: true, Draft: "keep draft"}
			c.ProjectCache = map[string][]model.Thread{other: nil}
			c.SetProjects([]projectmodel.Project{{Name: "current", Path: c.Cwd}, {Name: "other", Path: other}})
			if background {
				c.SetThreads([]model.Thread{keep})
				c.ProjectCache[other] = []model.Thread{target}
				c.SelectThread(keep)
			} else {
				c.SetThreads([]model.Thread{target, keep})
				c.SetActive(target.ID)
				c.RememberDraft("old draft")
			}
			c.ProjectCache[c.Cwd] = c.Threads()
			c.DeleteThread(target)

			if c.ThreadWithID(target.ID).ID != 0 {
				t.Fatal("deleted conversation is still listed")
			}
			if c.ThreadWithID(keep.ID).ID != keep.ID || c.Active() != keep.ID {
				t.Fatal("deletion removed or deselected the other conversation")
			}
			if c.Draft() != keep.Draft {
				t.Fatal("deletion mixed the conversations' drafts")
			}
			if cached := threadlist.Load(); len(cached[other]) != 0 || len(cached[c.Cwd]) != 1 || cached[c.Cwd][0].ID != keep.ID {
				t.Fatal("deletion was not saved to the correct project's cache")
			}
		})
	}
}
