package main

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
)

type desktopShortcut struct {
	mods  ui.Modifiers
	key   ui.Key
	name  string
	title string
	run   func()
}

// The help panel and keyboard handlers use the same bindings. Editing keys
// (copy, paste, undo, save, selection) stay with the focused input/editor.
func (app *chatApp) desktopShortcuts() []desktopShortcut {
	return []desktopShortcut{
		{ui.Cmd, ui.KeyN, "N", "New thread", app.newThread},
		{ui.Cmd, ui.KeyK, "K", "Search / Switch conversations", app.openConversationSwitcher},
		{ui.Cmd | ui.Shift, ui.KeyP, "P", "Command palette", app.openCommandPalette},
		{ui.Cmd, ui.KeyL, "L", "Focus message input", func() {
			app.section = ""
			app.closeMenus()
			app.focusComposer = true
		}},
		{ui.Cmd, ui.KeyB, "B", "Show / Hide sidebar", func() { app.sidebar = !app.sidebar }},
		{ui.Cmd | ui.Alt, ui.KeyB, "B", "Show / Hide right panel", func() {
			p := app.panel()
			if p.visible {
				app.hidePanel(p)
			} else {
				app.openPanel(p)
			}
		}},
		{ui.Cmd, ui.KeyComma, ",", "Open settings", func() { app.closeMenus(); app.section = "Settings" }},
		{ui.Cmd | ui.Shift, ui.KeyT, "T", "Open terminal", func() {
			app.closeMenus()
			app.section = ""
			app.openSurface(app.panel(), surfaceTerminal)
		}},
		{ui.Cmd | ui.Shift, ui.KeyO, "O", "Add attachment", func() {
			if app.section == "" && !app.c.current().ReadOnly && !app.c.current().Loading {
				app.attachFiles()
			}
		}},
		{ui.Ctrl, ui.KeyTab, "Tab", "Next conversation in project", func() { app.cycleConversation(1) }},
		{ui.Ctrl | ui.Shift, ui.KeyTab, "Tab", "Previous conversation in project", func() { app.cycleConversation(-1) }},
		{ui.Cmd, ui.KeyPeriod, ".", "Stop current response", func() {
			if t := app.c.current(); app.section == "" && t.Busy && !t.StopRequested {
				app.c.stop()
			}
		}},
		{0, ui.KeyF1, "F1", "Show shortcuts", func() { app.closeMenus(); app.shortcutsOpen = true }},
	}
}

func (app *chatApp) registerDesktopShortcuts(c *ui.Context) {
	for _, shortcut := range app.desktopShortcuts() {
		c.OnShortcut(shortcut.mods, shortcut.key, shortcut.run)
	}
}

func shortcutKeys(mods ui.Modifiers, key string) string {
	var parts []string
	if mods&ui.Ctrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if mods&ui.Alt != 0 {
		if ui.Cmd == ui.Super {
			parts = append(parts, "Option")
		} else {
			parts = append(parts, "Alt")
		}
	}
	if mods&ui.Shift != 0 {
		parts = append(parts, "Shift")
	}
	if mods&ui.Super != 0 {
		parts = append(parts, "⌘")
	}
	return strings.Join(append(parts, key), "+")
}

func (app *chatApp) cycleConversation(step int) {
	threads := displayedThreads(app.c.threads(), true)
	if len(threads) == 0 {
		return
	}
	index := -1
	for i, thread := range threads {
		if thread.ID == app.c.active() {
			index = i
			break
		}
	}
	if index < 0 {
		index = 0
		if step < 0 {
			index = len(threads) - 1
		}
	} else {
		index = (index + step + len(threads)) % len(threads)
	}
	app.selectThread(threads[index])
}

type conversationSwitcher struct {
	open     bool
	query    string
	selected int
	list     ui.ListState
}

func (app *chatApp) openConversationSwitcher() {
	app.closeMenus()
	app.switcher = conversationSwitcher{open: true}
}

// Search only metadata, including conversations whose first send is still
// starting and has not received a backend session ID yet.
func (app *chatApp) switcherChoices(query string) []threadReference {
	query = strings.ToLower(strings.TrimSpace(query))
	var choices []threadReference
	seen := map[int]bool{}
	for _, project := range app.c.threadRefProjects() {
		name := firstNonEmpty(project.Name, filepath.Base(project.Path))
		for _, thread := range project.Threads {
			if thread.ID == 0 || seen[thread.ID] {
				continue
			}
			seen[thread.ID] = true
			if query != "" && !strings.Contains(strings.ToLower(threadRefTitle(thread)+"\n"+thread.ServerID+"\n"+name), query) {
				continue
			}
			choices = append(choices, threadReference{Thread: thread, ProjectName: name})
		}
	}
	sort.SliceStable(choices, func(i, j int) bool { return choices[i].LastActivityAt > choices[j].LastActivityAt })
	return choices
}

func (app *chatApp) conversationSwitcherView(c *ui.Context) {
	s := &app.switcher
	ui.DialogBase(c.Key("conversation-switcher"), &s.open, func(backdrop, box ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.30))
		box.Column().Width(560).MaxWidthPercent(90).Padding(20).Gap(12).Background(panel).Border(1, border).Radius(14).TextColor(ink)
		ui.Text(c, tr("Switch conversations")).FontSize(18).FontWeight(600)
		input := ui.TextInput(c.Key("query"), &s.query).FillWidth().Label(tr("Search all conversations")).Placeholder(tr("Search by title, project, or conversation ID")).AutoFocus()
		if input.Changed() {
			s.selected = 0
			s.list.ScrollTo(0, ui.Start)
		}
		choices := app.switcherChoices(s.query)
		s.selected = max(0, min(s.selected, len(choices)-1))
		choose := func(index int) {
			s.open = false
			app.selectThread(choices[index].Thread)
		}
		if input.Shortcut(ui.Cmd, ui.KeyK) {
			s.open = false
		}
		if len(choices) > 0 {
			for _, key := range []ui.Key{ui.KeyUp, ui.KeyDown} {
				if input.Shortcut(0, key) {
					step := 1
					if key == ui.KeyUp {
						step = -1
					}
					s.selected = (s.selected + step + len(choices)) % len(choices)
					s.list.ScrollIntoView(s.selected)
				}
			}
			if input.Submitted() {
				choose(s.selected)
			}
			s.list.Key = func(i int) any { return choices[i].ID }
			ui.List(c.Key("results"), &s.list, len(choices), func(i int) {
				choice := choices[i]
				row := quiet(c.Key(choice.ID)).Column().FillWidth().Height(56).Padding(8, 12).Gap(4).AlignItems(ui.Start).Label(threadRefTitle(choice.Thread))
				if i == s.selected {
					row.Background(chosen)
				}
				row.Children(func() {
					ui.Text(c, threadRefTitle(choice.Thread)).FillWidth().SingleLine().Ellipsis("…")
					ui.Text(c, choice.ProjectName+" · "+backendOf(choice.Thread).Name()).FontSize(11).TextColor(muted).SingleLine()
				})
				if row.Clicked() {
					choose(i)
				}
			}).FillWidth().Height(float32(min(6, len(choices)) * 56))
		} else {
			mutedText(c, tr("No matching conversations")).Padding(16)
		}
		mutedText(c, tr("↑ ↓ Select · Enter Open · Esc Cancel")).FontSize(11)
	})
}

func (app *chatApp) shortcutHelpView(c *ui.Context) {
	ui.DialogBase(c.Key("keyboard-shortcuts"), &app.shortcutsOpen, func(backdrop, box ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.30))
		box.Column().Width(560).MaxWidthPercent(90).MaxHeightPercent(85).Padding(24).Gap(16).Background(panel).Border(1, border).Radius(14).TextColor(ink)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("Keyboard shortcuts")).FontSize(18).FontWeight(600).Grow(1)
			if toolButton(c.Key("close"), "x", tr("Close keyboard shortcuts")).Clicked() {
				app.shortcutsOpen = false
			}
		})
		ui.Scroll(c.Key("bindings")).FillWidth().MaxHeight(440).MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().Gap(12).Children(func() {
				row := func(title, keys string) {
					ui.Row(c).FillWidth().Gap(16).Children(func() {
						ui.Text(c, title).Grow(1)
						ui.Text(c, keys).TextColor(muted).Shrink(0)
					})
				}
				for _, shortcut := range app.desktopShortcuts() {
					row(tr(shortcut.title), shortcutKeys(shortcut.mods, shortcut.name))
				}
				row(tr("Send / Add to queue"), "Enter")
				row(tr("Insert a new line"), "Shift+Enter")
				row(tr("Send / Steer current response"), shortcutKeys(ui.Cmd, "Enter"))
				row(tr("Close dialog / Dismiss suggestions"), "Esc")
			})
		})
	})
}
