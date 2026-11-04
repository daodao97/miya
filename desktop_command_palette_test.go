package main

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDesktopCommandPaletteCommandsAndSidebar(t *testing.T) {
	app, tt := testApp(t)
	app.appearance = "light"
	if tt.HasText("Search conversations") || tt.HasText("Skills") {
		t.Fatal("removed sidebar entries are still visible")
	}
	app.c.rememberDraft("Keep my draft")
	tt.Frame()
	tt.Key(ui.Cmd|ui.Shift, ui.KeyP)
	if !app.commandPalette.open || !tt.Focused("Search commands") {
		t.Fatal("command palette did not open with its search focused")
	}
	saveReconnectSnapshot(t, tt, "command-palette-light")
	app.appearance = "dark"
	tt.Frame()
	saveReconnectSnapshot(t, tt, "command-palette-dark")
	tt.Type("skills")
	if err := tt.Click("Open skills"); err != nil {
		t.Fatal(err)
	}
	if app.commandPalette.open || app.section != "Skills" || app.c.draft() != "Keep my draft" {
		t.Fatal("command did not run or changed the draft")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyP)
	tt.Type("stng")
	tt.Key(0, ui.KeyEnter)
	if app.commandPalette.open || app.section != "Settings" {
		t.Fatal("fuzzy command search did not open settings")
	}
}

func TestDesktopCommandPaletteConversationNavigation(t *testing.T) {
	app, tt := testApp(t)
	var threads []Thread
	for i := 1; i <= 12; i++ {
		threads = append(threads, Thread{ID: i, Title: fmt.Sprintf("Review %02d", i), Draft: fmt.Sprintf("Draft %d", i), HistoryLoaded: true, LastActivityAt: int64(20 - i)})
	}
	app.c.setThreads(threads)
	app.c.selectThread(threads[0])
	tt.Frame()
	tt.Key(ui.Cmd|ui.Shift, ui.KeyP)
	tt.Type("Review conversation")
	for range 9 {
		tt.Key(0, ui.KeyDown)
	}
	if rect, ok := tt.Find("Review 10"); !ok || rect.Y < 80 || rect.Y > 490 {
		t.Fatalf("keyboard selection did not scroll into view: %v", rect)
	}
	tt.Key(0, ui.KeyUp)
	tt.Key(0, ui.KeyEnter)
	if app.commandPalette.open || app.c.active() != 9 || app.c.draft() != "Draft 9" || !tt.Focused("Message Codex") {
		t.Fatalf("incorrect conversation selection: active=%d draft=%q", app.c.active(), app.c.draft())
	}
	if app.c.threadWithID(1).Draft != "Draft 1" || len(app.c.current().Queue) != 0 {
		t.Fatal("palette changed or submitted a draft")
	}
}

func TestDesktopCommandPaletteDismissalAndModalSafety(t *testing.T) {
	for _, dismiss := range []string{"escape", "outside", "toggle", "empty"} {
		t.Run(dismiss, func(t *testing.T) {
			app, tt := testApp(t)
			tt.SetSize(780, 580)
			tt.Type("Keep this draft")
			tt.Key(ui.Cmd|ui.Shift, ui.KeyP)
			tt.Key(ui.Cmd, ui.KeyN)
			tt.Key(ui.Cmd, ui.KeyB)
			if app.c.draft() != "Keep this draft" || !app.sidebar {
				t.Fatal("a shortcut ran behind the palette")
			}
			switch dismiss {
			case "escape":
				tt.Key(0, ui.KeyEscape)
			case "outside":
				tt.ClickAt(20, 40)
			case "toggle":
				tt.Key(ui.Cmd|ui.Shift, ui.KeyP)
			case "empty":
				tt.Type("no-match-9382")
				tt.Key(0, ui.KeyEnter)
				if !app.commandPalette.open || !tt.HasText("No matching commands or conversations") {
					t.Fatal("empty results executed a stale selection")
				}
				tt.Key(0, ui.KeyEscape)
			}
			tt.Frame()
			if app.commandPalette.open || !tt.Focused("Message Codex") || app.c.draft() != "Keep this draft" {
				t.Fatal("dismissal lost composer focus or changed the draft")
			}
		})
	}
	app, tt := testApp(t)
	app.openProjectDialog()
	tt.Frame()
	tt.Key(ui.Cmd|ui.Shift, ui.KeyP)
	if app.commandPalette.open || !app.projectDialog {
		t.Fatal("palette opened behind another dialog")
	}
}

func TestDesktopCommandPaletteOpensAnotherDialog(t *testing.T) {
	app, tt := testApp(t)
	tt.Key(ui.Cmd|ui.Shift, ui.KeyP)
	tt.Type("add remote workspace")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if app.commandPalette.open || !app.workspaceHub.open || !tt.Focused("Workspace name") {
		t.Fatal("palette did not hand focus to the workspace dialog")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Key(ui.Cmd|ui.Shift, ui.KeyP)
	if app.commandPalette.query != "" {
		t.Fatal("reopening retained the previous filter")
	}
}
