package main

import (
	"path/filepath"
	"testing"
)

func TestSidebarDeletesConversationInItsProject(t *testing.T) {
	for _, background := range []bool{false, true} {
		name := "current"
		if background {
			name = "background"
		}
		t.Run(name, func(t *testing.T) {
			app, tt := testApp(t)
			c := app.c
			other := filepath.Join(t.TempDir(), "other")
			target := Thread{ID: 1, ServerID: "target", Title: "Delete this conversation", Busy: true}
			keep := Thread{ID: 2, ServerID: "keep", Title: "Keep this conversation", HistoryLoaded: true, Draft: "keep draft"}
			c.projectCache = map[string][]Thread{other: nil}
			c.setProjects([]Project{{Name: "current", Path: c.cwd}, {Name: "other", Path: other}})
			if background {
				c.setThreads([]Thread{keep})
				c.projectCache[other] = []Thread{target}
				c.selectThread(keep)
			} else {
				c.setThreads([]Thread{target, keep})
				c.setActive(target.ID)
				c.rememberDraft("old draft")
			}
			c.projectCache[c.cwd] = c.threads()
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
			if c.threadWithID(target.ID).ID != 0 || tt.HasText(target.Title) {
				t.Fatal("deleted conversation is still listed")
			}
			if c.threadWithID(keep.ID).ID != keep.ID || c.active() != keep.ID {
				t.Fatal("deletion removed or deselected the other conversation")
			}
			if c.draft() != keep.Draft {
				t.Fatal("deletion mixed the conversations' drafts")
			}
			if cached := loadThreadListCache(); len(cached[other]) != 0 || len(cached[c.cwd]) != 1 || cached[c.cwd][0].ID != keep.ID {
				t.Fatal("deletion was not saved to the correct project's cache")
			}
		})
	}
}
