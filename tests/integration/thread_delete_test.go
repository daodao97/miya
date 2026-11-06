package integration

import (
	"path/filepath"
	"testing"

	threadlist "miya/internal/chat/history"
	"miya/internal/chat/model"
	"miya/internal/controller"
	projectmodel "miya/internal/project/model"
)

func TestSidebarDeletesConversationInItsProject(t *testing.T) {
	for _, background := range []bool{false, true} {
		name := "current"
		if background {
			name = "background"
		}
		t.Run(name, func(t *testing.T) {
			app, tt := testApp(t)
			c := app.C.(*controller.Controller)
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
			tt.Frame()
			r, ok := tt.Find(target.Title)
			if !ok {
				t.Fatal("target conversation missing")
			}
			tt.Move(r.X+8, r.Y+r.H/2)
			if err := tt.Click("Conversation actions"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("Delete conversation"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if c.ThreadWithID(target.ID).ID != 0 || tt.HasText(target.Title) {
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
