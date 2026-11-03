package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDesktopShortcutsNavigationAndLayout(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	c.setThreads([]Thread{{ID: 1, Title: "Current", HistoryLoaded: true}})
	c.setActive(1)
	tt.Frame()
	tt.Type("keep this draft")
	tt.Key(ui.Cmd, ui.KeyB)
	if app.sidebar || c.draft() != "keep this draft" {
		t.Fatal("sidebar shortcut failed or changed the draft")
	}
	tt.Key(ui.Cmd, ui.KeyB)
	if !app.sidebar {
		t.Fatal("sidebar shortcut did not toggle back")
	}
	tt.Key(ui.Cmd|ui.Alt, ui.KeyB)
	if !app.panel().visible {
		t.Fatal("panel shortcut did not open the panel")
	}
	tt.Key(ui.Cmd|ui.Alt, ui.KeyB)
	if app.panel().visible {
		t.Fatal("panel shortcut did not hide the panel")
	}
	tt.Key(ui.Cmd, ui.KeyComma)
	if app.section != "Settings" || c.draft() != "keep this draft" {
		t.Fatal("settings shortcut lost the conversation draft")
	}
	tt.Key(ui.Cmd, ui.KeyL)
	if app.section != "" || !tt.Focused("Message Codex") {
		t.Fatal("focus shortcut did not return to the composer")
	}
	tt.Key(ui.Cmd, ui.KeyN)
	if c.active() != 0 || c.draft() != "" || !tt.Focused("Message Codex") {
		t.Fatal("new-conversation shortcut did not focus an empty draft")
	}
	if c.threadWithID(1).Draft != "keep this draft" {
		t.Fatal("new conversation erased the old conversation's draft")
	}
}

func TestDesktopTerminalShortcut(t *testing.T) {
	app, tt := testApp(t)
	// Leave terminal startup pending so this checks keyboard routing without
	// launching a shell. Terminal focus after startup has its own test.
	old := goAsync
	starts := 0
	goAsync = func(func()) { starts++ }
	t.Cleanup(func() { goAsync = old })
	app.c.rememberDraft("keep this draft")
	app.section = "Settings"
	tt.Frame()
	for i := 1; i <= 2; i++ {
		tt.Key(ui.Cmd|ui.Shift, ui.KeyT)
		p := app.panel()
		if app.section != "" || !p.visible || p.cwd != app.c.cwd || len(p.surfaces) != i || p.active != i-1 {
			t.Fatal("terminal shortcut did not open a new tab in the current workspace")
		}
		if s := p.surfaces[p.active]; s.kind != surfaceTerminal || !s.starting || starts != i {
			t.Fatal("terminal shortcut did not start the new terminal")
		}
		if app.c.draft() != "keep this draft" {
			t.Fatal("terminal shortcut changed the conversation draft")
		}
	}
	tt.Key(0, ui.KeyF1)
	if !tt.HasText("打开终端") || !tt.HasText(shortcutKeys(ui.Cmd|ui.Shift, "T")) {
		t.Fatal("shortcut help does not list the terminal shortcut")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyT)
	if len(app.panel().surfaces) != 2 || starts != 2 {
		t.Fatal("terminal shortcut ran behind the help dialog")
	}
}

func TestDesktopConversationSwitcherKeyboard(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	var threads []Thread
	for i := 1; i <= 9; i++ {
		threads = append(threads, Thread{ID: i, Title: fmt.Sprintf("Conversation %d", i), Draft: fmt.Sprintf("draft %d", i), HistoryLoaded: true, LastActivityAt: int64(10 - i)})
	}
	c.setThreads(threads)
	c.selectThread(threads[0])
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	if !app.switcher.open || !tt.Focused("搜索全部对话") {
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
	if app.switcher.open || c.active() != 8 || c.draft() != "draft 8" || !tt.Focused("Message Codex") {
		t.Fatalf("wrong conversation or focus: active=%d draft=%q open=%v", c.active(), c.draft(), app.switcher.open)
	}
	if c.threadWithID(1).Draft != "draft 1" || len(c.current().Queue) != 0 {
		t.Fatal("choosing a conversation changed or submitted a draft")
	}
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("no matching conversation")
	if !tt.HasText("没有匹配的对话") {
		t.Fatal("empty search has no feedback")
	}
	tt.Key(0, ui.KeyEnter)
	if !app.switcher.open || c.active() != 8 {
		t.Fatal("Enter on an empty result changed the conversation")
	}
	tt.Key(0, ui.KeyEscape)
	if app.switcher.open || c.draft() != "draft 8" {
		t.Fatalf("canceling search: open=%v draft=%q query=%q", app.switcher.open, c.draft(), app.switcher.query)
	}
}

func TestDesktopConversationSearchAcrossProjects(t *testing.T) {
	app, _ := testApp(t)
	c := app.c
	c.setThreads([]Thread{{ID: 1, Title: "Starting conversation", LastActivityAt: 10}})
	c.setProjects(append(c.projects(), Project{Name: "Other project", Path: "/other"}))
	c.projectCache = map[string][]Thread{
		c.cwd:    {{ID: 1, Title: "Stale title"}},
		"/other": {{ID: 2, ServerID: "session-api", Title: "API review", LastActivityAt: 20}},
	}
	for _, query := range []string{"other PROJECT", "api REVIEW", "SESSION-api"} {
		choices := app.switcherChoices(query)
		if len(choices) != 1 || choices[0].ID != 2 {
			t.Fatalf("query %q did not find the other project: %+v", query, choices)
		}
	}
	if choices := app.switcherChoices("starting"); len(choices) != 1 || choices[0].ID != 1 {
		t.Fatal("a conversation without a backend session ID was omitted")
	}
	if choices := app.switcherChoices("stale"); len(choices) != 0 {
		t.Fatal("search used stale current-project metadata")
	}
}

func TestDesktopConversationCyclePreservesDrafts(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	c.setThreads([]Thread{{ID: 1, Title: "Oldest", LastActivityAt: 10}, {ID: 2, Title: "Newest", LastActivityAt: 30}, {ID: 3, Title: "Middle", LastActivityAt: 20}})
	c.selectThread(c.threadWithID(2))
	c.rememberDraft("draft in newest")
	tt.Frame()
	for _, want := range []int{3, 1, 2} {
		tt.Key(ui.Ctrl, ui.KeyTab)
		if c.active() != want {
			t.Fatalf("next conversation = %d, want %d", c.active(), want)
		}
	}
	if c.draft() != "draft in newest" {
		t.Fatal("cycling lost the saved draft")
	}
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyTab)
	if c.active() != 1 {
		t.Fatal("previous conversation did not wrap")
	}
}

func TestDesktopConversationSwitcherChangesProject(t *testing.T) {
	app, tt := testApp(t)
	c := app.c
	first, second := c.cwd, t.TempDir()
	c.setProjects([]Project{{Name: "First", Path: first}, {Name: "Second", Path: second}})
	c.setThreads([]Thread{{ID: 1, Title: "First conversation", HistoryLoaded: true}})
	c.projectCache = map[string][]Thread{second: {{ID: 2, Title: "Second conversation", HistoryLoaded: true, Draft: "second draft"}}}
	c.selectThread(c.threadWithID(1))
	c.rememberDraft("first draft")
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("Second")
	tt.Key(0, ui.KeyEnter)
	if c.cwd != second || c.active() != 2 || c.draft() != "second draft" || app.projectName != "Second" {
		t.Fatalf("search did not switch projects: cwd=%q active=%d draft=%q", c.cwd, c.active(), c.draft())
	}
	if c.projectCache[first][0].Draft != "first draft" {
		t.Fatal("cross-project switch lost the original draft")
	}
}

func TestDesktopShortcutsRespectModal(t *testing.T) {
	app, tt := testApp(t)
	app.c.setThreads([]Thread{{ID: 1, Title: "Current"}})
	app.c.setActive(1)
	tt.SetSize(780, 580)
	tt.Frame()
	tt.Key(0, ui.KeyF1)
	if !app.shortcutsOpen || !tt.HasText("键盘快捷键") || !tt.HasText(shortcutKeys(ui.Cmd, "N")) {
		t.Fatal("shortcut help did not open or show bindings")
	}
	saveReconnectSnapshot(t, tt, "keyboard-shortcuts")
	tt.Key(ui.Cmd, ui.KeyN)
	tt.Key(ui.Cmd, ui.KeyB)
	if app.c.active() != 1 || !app.sidebar {
		t.Fatal("a global shortcut ran behind the help dialog")
	}
	tt.Key(0, ui.KeyEscape)
	if app.shortcutsOpen {
		t.Fatal("Escape did not close shortcut help")
	}
	app.openProjectDialog()
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	if app.switcher.open || !app.projectDialog {
		t.Fatal("conversation search opened behind another dialog")
	}
}

func TestDesktopStopShortcutTargetsCurrent(t *testing.T) {
	app, tt := testApp(t)
	app.c.setThreads([]Thread{{ID: 1, Busy: true}, {ID: 2, Busy: true}})
	app.c.setActive(1)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyPeriod)
	if !app.c.threadWithID(1).StopRequested || app.c.threadWithID(2).StopRequested {
		t.Fatal("stop shortcut did not target only the current conversation")
	}
}

func TestComposerSendAndNewlineShortcuts(t *testing.T) {
	for _, mods := range []ui.Modifiers{0, ui.Cmd, ui.Shift} {
		t.Run(fmt.Sprint(mods), func(t *testing.T) {
			app, tt := testApp(t)
			c := app.c
			c.setThreads([]Thread{{ID: 1, Busy: true, HistoryLoaded: true}})
			c.setActive(1)
			tt.Frame()
			tt.Type("message")
			tt.Key(mods, ui.KeyEnter)
			if mods == ui.Shift {
				if c.draft() != "message\n" || len(c.current().Queue) != 0 {
					t.Fatal("Shift+Enter submitted instead of inserting a newline")
				}
			} else if c.draft() != "" || len(c.current().Queue) != 1 || c.current().Queue[0].Text != "message" {
				t.Fatal("send shortcut did not submit the message")
			}
		})
	}
	app, tt := testApp(t)
	tt.Type("/status")
	tt.Key(0, ui.KeyEscape) // leave slash completion before submitting
	tt.Key(ui.Cmd, ui.KeyEnter)
	if !strings.Contains(app.notice, "Model: gpt-test") {
		t.Fatal("Cmd+Enter did not submit an idle composer's command")
	}
}
