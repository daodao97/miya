package main

import (
	fmt "fmt"
	appmodel "miya/internal/appmodel"
	controller "miya/internal/controller"
	testing "testing"

	ui "github.com/egoist/mygo/ui"
)

func TestDesktopCommandPaletteCommandsAndSidebar(t *testing.T) {
	app, tt := testApp(t)
	app.Appearance = "light"
	if tt.HasText("Search conversations") || tt.HasText("Skills") {
		t.Fatal("removed sidebar entries are still visible")
	}
	app.C.(*controller.Controller).RememberDraft("Keep my draft")
	tt.Frame()
	tt.Key(ui.Cmd|ui.Shift, ui.KeyP)
	if !app.CommandPalette.Open || !tt.Focused("Search commands") {
		t.Fatal("command palette did not open with its search focused")
	}
	saveReconnectSnapshot(t, tt, "command-palette-light")
	app.Appearance = "dark"
	tt.Frame()
	saveReconnectSnapshot(t, tt, "command-palette-dark")
	tt.Type("skills")
	if err := tt.Click("Open skills"); err != nil {
		t.Fatal(err)
	}
	if app.CommandPalette.Open || app.Section != "Skills" || app.C.(*controller.Controller).Draft() != "Keep my draft" {
		t.Fatal("command did not run or changed the draft")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyP)
	tt.Type("stng")
	tt.Key(0, ui.KeyEnter)
	if app.CommandPalette.Open || app.Section != "Settings" {
		t.Fatal("fuzzy command search did not open settings")
	}
}

func TestDesktopCommandPaletteConversationNavigation(t *testing.T) {
	app, tt := testApp(t)
	var threads []appmodel.Thread
	for i := 1; i <= 12; i++ {
		threads = append(threads, appmodel.Thread{ID: i, Title: fmt.Sprintf("Review %02d", i), Draft: fmt.Sprintf("Draft %d", i), HistoryLoaded: true, LastActivityAt: int64(20 - i)})
	}
	app.C.(*controller.Controller).SetThreads(threads)
	app.C.(*controller.Controller).SelectThread(threads[0])
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
	if app.CommandPalette.Open || app.C.(*controller.Controller).Active() != 9 || app.C.(*controller.Controller).Draft() != "Draft 9" || !tt.Focused("Message Codex") {
		t.Fatalf("incorrect conversation selection: active=%d draft=%q", app.C.(*controller.Controller).Active(), app.C.(*controller.Controller).Draft())
	}
	if app.C.(*controller.Controller).ThreadWithID(1).Draft != "Draft 1" || len(app.C.(*controller.Controller).Current().Queue) != 0 {
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
			if app.C.(*controller.Controller).Draft() != "Keep this draft" || !app.Sidebar {
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
				if !app.CommandPalette.Open || !tt.HasText("No matching commands or conversations") {
					t.Fatal("empty results executed a stale selection")
				}
				tt.Key(0, ui.KeyEscape)
			}
			tt.Frame()
			if app.CommandPalette.Open || !tt.Focused("Message Codex") || app.C.(*controller.Controller).Draft() != "Keep this draft" {
				t.Fatal("dismissal lost composer focus or changed the draft")
			}
		})
	}
	app, tt := testApp(t)
	app.OpenProjectDialog()
	tt.Frame()
	tt.Key(ui.Cmd|ui.Shift, ui.KeyP)
	if app.CommandPalette.Open || !app.ProjectDialog {
		t.Fatal("palette opened behind another dialog")
	}
}

func TestDesktopCommandPaletteOpensAnotherDialog(t *testing.T) {
	app, tt := testApp(t)
	tt.Key(ui.Cmd|ui.Shift, ui.KeyP)
	tt.Type("add remote workspace")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if app.CommandPalette.Open || !app.WorkspaceHub.Open || !tt.Focused("Workspace name") {
		t.Fatal("palette did not hand focus to the workspace dialog")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Key(ui.Cmd|ui.Shift, ui.KeyP)
	if app.CommandPalette.Query != "" {
		t.Fatal("reopening retained the previous filter")
	}
}
