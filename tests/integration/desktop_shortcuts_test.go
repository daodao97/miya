package integration

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
	"miya/internal/controller"
	projectmodel "miya/internal/project/model"
	desktopui "miya/internal/ui"
	"miya/internal/uiruntime"
)

func TestDesktopShortcutsNavigationAndLayout(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetThreads([]model.Thread{{ID: 1, Title: "Current", HistoryLoaded: true}})
	c.SetActive(1)
	tt.Frame()
	tt.Type("keep this draft")
	tt.Key(ui.Cmd, ui.KeyB)
	if app.Sidebar || c.Draft() != "keep this draft" {
		t.Fatal("sidebar shortcut failed or changed the draft")
	}
	tt.Key(ui.Cmd, ui.KeyB)
	if !app.Sidebar {
		t.Fatal("sidebar shortcut did not toggle back")
	}
	tt.Key(ui.Cmd|ui.Alt, ui.KeyB)
	if !app.Panel().Visible {
		t.Fatal("panel shortcut did not open the panel")
	}
	tt.Key(ui.Cmd|ui.Alt, ui.KeyB)
	if app.Panel().Visible {
		t.Fatal("panel shortcut did not hide the panel")
	}
	tt.Key(ui.Cmd, ui.KeyComma)
	if app.Section != "Settings" || c.Draft() != "keep this draft" {
		t.Fatal("settings shortcut lost the conversation draft")
	}
	tt.Key(ui.Cmd, ui.KeyL)
	if app.Section != "" || !tt.Focused("Message Codex") {
		t.Fatal("focus shortcut did not return to the composer")
	}
	tt.Key(ui.Cmd, ui.KeyN)
	if c.Active() != 0 || c.Draft() != "" || !tt.Focused("Message Codex") {
		t.Fatal("new-conversation shortcut did not focus an empty draft")
	}
	if c.ThreadWithID(1).Draft != "keep this draft" {
		t.Fatal("new conversation erased the old conversation's draft")
	}
}

func TestDesktopTerminalShortcut(t *testing.T) {
	app, tt := testApp(t)
	// Leave terminal startup pending so this checks keyboard routing without
	// launching a shell. Terminal focus after startup has its own test.
	old := uiruntime.GoAsync
	starts := 0
	uiruntime.GoAsync = func(func()) { starts++ }
	t.Cleanup(func() { uiruntime.GoAsync = old })
	app.C.(*controller.Controller).RememberDraft("keep this draft")
	app.Section = "Settings"
	tt.Frame()
	for i := 1; i <= 2; i++ {
		tt.Key(ui.Cmd|ui.Shift, ui.KeyT)
		p := app.Panel()
		if app.Section != "" || !p.Visible || p.Cwd != app.C.(*controller.Controller).Cwd || len(p.Surfaces) != i || p.Active != i-1 {
			t.Fatal("terminal shortcut did not open a new tab in the current workspace")
		}
		if s := p.Surfaces[p.Active]; s.Kind != desktopui.SurfaceTerminal || !s.Starting || starts != i {
			t.Fatal("terminal shortcut did not start the new terminal")
		}
		if app.C.(*controller.Controller).Draft() != "keep this draft" {
			t.Fatal("terminal shortcut changed the conversation draft")
		}
	}
	tt.Key(0, ui.KeyF1)
	if !tt.HasText("Open terminal") || !tt.HasText(desktopui.ShortcutKeys(ui.Cmd|ui.Shift, "T")) {
		t.Fatal("shortcut help does not list the terminal shortcut")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyT)
	if len(app.Panel().Surfaces) != 2 || starts != 2 {
		t.Fatal("terminal shortcut ran behind the help dialog")
	}
}

func TestDesktopConversationSwitcherKeyboard(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	var threads []model.Thread
	for i := 1; i <= 9; i++ {
		threads = append(threads, model.Thread{ID: i, Title: fmt.Sprintf("Conversation %d", i), Draft: fmt.Sprintf("draft %d", i), HistoryLoaded: true, LastActivityAt: int64(10 - i)})
	}
	c.SetThreads(threads)
	c.SelectThread(threads[0])
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	if !app.Switcher.Open || !tt.Focused("Search all conversations") {
		t.Fatal("conversation switcher did not open with search focused")
	}
	tt.Type("Conversation")
	saveReconnectSnapshot(t, tt, "conversation-switcher")
	for range 7 {
		tt.Key(0, ui.KeyDown)
	}
	if _, ok := tt.Find("Conversation 8"); !ok {
		t.Fatal("keyboard selection did not reveal a result beyond the first page")
	}
	tt.Key(0, ui.KeyEnter)
	if app.Switcher.Open || c.Active() != 8 || c.Draft() != "draft 8" || !tt.Focused("Message Codex") {
		t.Fatalf("wrong conversation or focus: active=%d draft=%q open=%v", c.Active(), c.Draft(), app.Switcher.Open)
	}
	if c.ThreadWithID(1).Draft != "draft 1" || len(c.Current().Queue) != 0 {
		t.Fatal("choosing a conversation changed or submitted a draft")
	}
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("no matching conversation")
	if !tt.HasText("No matching conversations") {
		t.Fatal("empty search has no feedback")
	}
	tt.Key(0, ui.KeyEnter)
	if !app.Switcher.Open || c.Active() != 8 {
		t.Fatal("Enter on an empty result changed the conversation")
	}
	tt.Key(0, ui.KeyEscape)
	if app.Switcher.Open || c.Draft() != "draft 8" {
		t.Fatalf("canceling search: open=%v draft=%q query=%q", app.Switcher.Open, c.Draft(), app.Switcher.Query)
	}
}

func TestDesktopConversationSearchAcrossProjects(t *testing.T) {
	app, _ := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetThreads([]model.Thread{{ID: 1, Title: "Starting conversation", LastActivityAt: 10}})
	c.SetProjects(append(c.Projects(), projectmodel.Project{Name: "Other project", Path: "/other"}))
	c.ProjectCache = map[string][]model.Thread{
		c.Cwd:    {{ID: 1, Title: "Stale title"}},
		"/other": {{ID: 2, ServerID: "session-api", Title: "API review", LastActivityAt: 20}},
	}
	for _, query := range []string{"other PROJECT", "api REVIEW", "SESSION-api"} {
		choices := app.SwitcherChoices(query)
		if len(choices) != 1 || choices[0].ID != 2 {
			t.Fatalf("query %q did not find the other project: %+v", query, choices)
		}
	}
	if choices := app.SwitcherChoices("starting"); len(choices) != 1 || choices[0].ID != 1 {
		t.Fatal("a conversation without a backend session ID was omitted")
	}
	if choices := app.SwitcherChoices("stale"); len(choices) != 0 {
		t.Fatal("search used stale current-project metadata")
	}
}

func TestDesktopConversationCyclePreservesDrafts(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	c.SetThreads([]model.Thread{{ID: 1, Title: "Oldest", LastActivityAt: 10}, {ID: 2, Title: "Newest", LastActivityAt: 30}, {ID: 3, Title: "Middle", LastActivityAt: 20}})
	c.SelectThread(c.ThreadWithID(2))
	c.RememberDraft("draft in newest")
	tt.Frame()
	for _, want := range []int{3, 1, 2} {
		tt.Key(ui.Ctrl, ui.KeyTab)
		if c.Active() != want {
			t.Fatalf("next conversation = %d, want %d", c.Active(), want)
		}
	}
	if c.Draft() != "draft in newest" {
		t.Fatal("cycling lost the saved draft")
	}
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyTab)
	if c.Active() != 1 {
		t.Fatal("previous conversation did not wrap")
	}
}

func TestDesktopConversationSwitcherChangesProject(t *testing.T) {
	app, tt := testApp(t)
	c := app.C.(*controller.Controller)
	first, second := c.Cwd, t.TempDir()
	c.SetProjects([]projectmodel.Project{{Name: "First", Path: first}, {Name: "Second", Path: second}})
	c.SetThreads([]model.Thread{{ID: 1, Title: "First conversation", HistoryLoaded: true}})
	c.ProjectCache = map[string][]model.Thread{second: {{ID: 2, Title: "Second conversation", HistoryLoaded: true, Draft: "second draft"}}}
	c.SelectThread(c.ThreadWithID(1))
	c.RememberDraft("first draft")
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("Second")
	tt.Key(0, ui.KeyEnter)
	if c.Cwd != second || c.Active() != 2 || c.Draft() != "second draft" || app.ProjectName != "Second" {
		t.Fatalf("search did not switch projects: cwd=%q active=%d draft=%q", c.Cwd, c.Active(), c.Draft())
	}
	if c.ProjectCache[first][0].Draft != "first draft" {
		t.Fatal("cross-project switch lost the original draft")
	}
}

func TestDesktopShortcutsRespectModal(t *testing.T) {
	app, tt := testApp(t)
	app.C.(*controller.Controller).SetThreads([]model.Thread{{ID: 1, Title: "Current"}})
	app.C.(*controller.Controller).SetActive(1)
	tt.SetSize(780, 580)
	tt.Frame()
	tt.Key(0, ui.KeyF1)
	if !app.ShortcutsOpen || !tt.HasText("Keyboard shortcuts") || !tt.HasText(desktopui.ShortcutKeys(ui.Cmd, "N")) {
		t.Fatal("shortcut help did not open or show bindings")
	}
	saveReconnectSnapshot(t, tt, "keyboard-shortcuts")
	tt.Key(ui.Cmd, ui.KeyN)
	tt.Key(ui.Cmd, ui.KeyB)
	if app.C.(*controller.Controller).Active() != 1 || !app.Sidebar {
		t.Fatal("a global shortcut ran behind the help dialog")
	}
	tt.Key(0, ui.KeyEscape)
	if app.ShortcutsOpen {
		t.Fatal("Escape did not close shortcut help")
	}
	app.OpenProjectDialog()
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	if app.Switcher.Open || !app.ProjectDialog {
		t.Fatal("conversation search opened behind another dialog")
	}
}

func TestDesktopStopShortcutTargetsCurrent(t *testing.T) {
	app, tt := testApp(t)
	app.C.(*controller.Controller).SetThreads([]model.Thread{{ID: 1, Busy: true}, {ID: 2, Busy: true}})
	app.C.(*controller.Controller).SetActive(1)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyPeriod)
	if !app.C.(*controller.Controller).ThreadWithID(1).StopRequested || app.C.(*controller.Controller).ThreadWithID(2).StopRequested {
		t.Fatal("stop shortcut did not target only the current conversation")
	}
}

func TestComposerSendAndNewlineShortcuts(t *testing.T) {
	for _, mods := range []ui.Modifiers{0, ui.Cmd, ui.Shift} {
		t.Run(fmt.Sprint(mods), func(t *testing.T) {
			app, tt := testApp(t)
			c := app.C.(*controller.Controller)
			c.SetThreads([]model.Thread{{ID: 1, Busy: true, HistoryLoaded: true}})
			c.SetActive(1)
			tt.Frame()
			tt.Type("message")
			tt.Key(mods, ui.KeyEnter)
			if mods == ui.Shift {
				if c.Draft() != "message\n" || len(c.Current().Queue) != 0 {
					t.Fatal("Shift+Enter submitted instead of inserting a newline")
				}
			} else if c.Draft() != "" || len(c.Current().Queue) != 1 || c.Current().Queue[0].Text != "message" {
				t.Fatal("send shortcut did not submit the message")
			}
		})
	}
	app, tt := testApp(t)
	tt.Type("/status")
	tt.Key(0, ui.KeyEscape) // leave slash completion before submitting
	tt.Key(ui.Cmd, ui.KeyEnter)
	if !strings.Contains(app.Notice, "Model: gpt-test") {
		t.Fatal("Cmd+Enter did not submit an idle composer's command")
	}
}
