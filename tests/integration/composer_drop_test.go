package integration

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"miya/internal/chat/model"
	"miya/internal/controller"
)

func TestComposerDropsFilesWithoutEditingOrSendingDraft(t *testing.T) {
	for _, target := range []string{"Message Codex", "Add context"} {
		t.Run(target, func(t *testing.T) {
			app, tt := testApp(t)
			c := app.C.(*controller.Controller)
			c.SetThreads([]model.Thread{{ID: 1, Busy: true, HistoryLoaded: true}})
			c.SetActive(1)
			c.RememberDraft("保留这段正文")
			paths := []string{filepath.Join(t.TempDir(), "notes with spaces.md"), filepath.Join(t.TempDir(), "测试.txt")}
			for _, path := range paths {
				if err := os.WriteFile(path, []byte("context"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			c.AddAttachments(paths[:1])
			tt.Frame()
			doc := app.Complete.Input.Doc
			doc.SetSelection(2, 4)
			tt.Frame()
			version := doc.Version()
			r, ok := tt.Find(target)
			if !ok {
				t.Fatalf("missing drop target %q", target)
			}
			if !tt.DropFiles(r.X+r.W/2, r.Y+r.H/2, paths...) {
				t.Fatal("composer rejected dropped files")
			}
			tt.Frame()
			if !slices.Equal(c.Attachments(), paths) {
				t.Fatalf("attachments were lost or duplicated: %v", c.Attachments())
			}
			if c.Draft() != "保留这段正文" || doc.Version() != version || len(c.Current().Queue) != 0 {
				t.Fatal("dropping attachments edited or submitted the draft")
			}
			if !tt.Focused("Message Codex") {
				t.Fatal("drop did not focus the composer")
			}
		})
	}
}

func TestComposerDoesNotDropFilesWhileLoadingOrReadOnly(t *testing.T) {
	for _, state := range []string{"loading", "read-only"} {
		t.Run(state, func(t *testing.T) {
			app, tt := testApp(t)
			c := app.C.(*controller.Controller)
			c.SetThreads([]model.Thread{{ID: 1, HistoryLoaded: true}})
			c.SetActive(1)
			tt.Frame()
			r, ok := tt.Find("Message Codex")
			if !ok {
				t.Fatal("composer missing")
			}
			c.Update(1, func(th *model.Thread) {
				th.Loading = state == "loading"
				th.ReadOnly = state == "read-only"
			})
			tt.Frame()
			if tt.DropFiles(r.X+r.W/2, r.Y+r.H/2, "/tmp/context.txt") || len(c.Attachments()) != 0 {
				t.Fatal("unavailable composer accepted dropped files")
			}
		})
	}
}
