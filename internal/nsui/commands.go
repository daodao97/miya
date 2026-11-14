package nsui

import (
	"runtime"
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/nswidgets"
)

// commands are what the menu bar and the window's shortcuts do.
type commands struct {
	newThread, openSettings, toggleSidebar, stop, openPalette, shortcuts func()
	// step selects the conversation step places after the current one in
	// the open project, before it when negative.
	step func(step int)
}

// bindCommands gives the window its menu bar on macOS and its shortcuts,
// as docs/keyboard-shortcuts.md lists them. The menu bar holds New
// conversation and Settings there, with their shortcuts; elsewhere the
// window binds them itself.
func bindCommands(cx *core.Scope, cmd commands) {
	if runtime.GOOS == "darwin" {
		// An effect, so the menu follows the language.
		cx.Effect(func() { desktop.SetApplicationMenu(cx, appMenu(cmd)) })
	} else {
		cx.OnKey("mod+n", cmd.newThread)
		cx.OnKey("mod+,", cmd.openSettings)
	}
	cx.OnKey("mod+k", cmd.openPalette)
	cx.OnKey("mod+shift+p", cmd.openPalette)
	cx.OnKey("mod+b", cmd.toggleSidebar)
	cx.OnKey("mod+.", cmd.stop)
	cx.OnKey("f1", cmd.shortcuts)
	cx.OnKey("ctrl+tab", func() { cmd.step(1) })
	cx.OnKey("ctrl+shift+tab", func() { cmd.step(-1) })
}

// appMenu is the macOS menu bar; the platform labels the role items.
func appMenu(cmd commands) []desktop.MenuItem {
	return []desktop.MenuItem{
		{Items: []desktop.MenuItem{
			{Role: desktop.MenuAbout},
			{Separator: true},
			{Label: tr("Settings…"), Shortcut: "mod+,", OnSelect: cmd.openSettings},
			{Separator: true},
			{Role: desktop.MenuHide}, {Role: desktop.MenuHideOthers}, {Role: desktop.MenuShowAll},
			{Separator: true},
			{Role: desktop.MenuQuit},
		}},
		{Label: tr("File"), Items: []desktop.MenuItem{
			{Label: tr("New conversation"), Shortcut: "mod+n", OnSelect: cmd.newThread},
			{Separator: true},
			{Role: desktop.MenuClose},
		}},
		{Label: tr("Edit"), Items: []desktop.MenuItem{
			{Role: desktop.MenuUndo}, {Role: desktop.MenuRedo},
			{Separator: true},
			{Role: desktop.MenuCut}, {Role: desktop.MenuCopy}, {Role: desktop.MenuPaste}, {Role: desktop.MenuSelectAll},
		}},
		{Label: tr("View"), Items: []desktop.MenuItem{
			{Label: tr("Search…"), Shortcut: "mod+k", OnSelect: cmd.openPalette},
			{Label: tr("Toggle sidebar"), Shortcut: "mod+b", OnSelect: cmd.toggleSidebar},
			{Separator: true},
			{Label: tr("Show shortcuts"), Shortcut: "f1", OnSelect: cmd.shortcuts},
		}},
		{Label: tr("Window"), Items: []desktop.MenuItem{
			{Role: desktop.MenuMinimize}, {Role: desktop.MenuZoom},
		}},
	}
}

// stepThread is the ID of the conversation step places from current among
// the open project's conversations, most recently active first, wrapping
// around; current itself when there is none other.
func stepThread(threads []model.Thread, current, step int) int {
	list := msgfmt.DisplayedThreads(threads, true)
	if len(list) == 0 {
		return current
	}
	at := -1
	for i, t := range list {
		if t.ID == current {
			at = i
			break
		}
	}
	if at < 0 {
		if step < 0 {
			return list[len(list)-1].ID
		}
		return list[0].ID
	}
	n := len(list)
	return list[((at+step)%n+n)%n].ID
}

// shortcutList is what the shortcuts dialog lists, in the form OnKey
// takes. Editing keys stay with the focused input or editor.
var shortcutList = []struct{ title, keys string }{
	{"New thread", "mod+n"},
	{"Search / Switch conversations", "mod+k"},
	{"Command palette", "mod+shift+p"},
	{"Show / Hide sidebar", "mod+b"},
	{"Show / Hide right panel", "mod+alt+b"},
	{"Open settings", "mod+,"},
	{"Next conversation in project", "ctrl+tab"},
	{"Previous conversation in project", "ctrl+shift+tab"},
	{"Stop current response", "mod+."},
	{"Show shortcuts", "f1"},
	{"Send / Add to queue", "enter"},
	{"Insert a new line", "shift+enter"},
	{"Close dialog / Dismiss suggestions", "escape"},
}

// keysLabel writes keys as the platform shows them.
func keysLabel(keys string) string {
	mac := runtime.GOOS == "darwin"
	var out []string
	for _, k := range strings.Split(keys, "+") {
		switch k {
		case "mod":
			if mac {
				k = "⌘"
			} else {
				k = "Ctrl"
			}
		case "alt":
			if mac {
				k = "Option"
			} else {
				k = "Alt"
			}
		case "shift", "ctrl", "enter", "tab":
			k = strings.ToUpper(k[:1]) + k[1:]
		case "escape":
			k = "Esc"
		default:
			k = strings.ToUpper(k)
		}
		out = append(out, k)
	}
	return strings.Join(out, "+")
}

// shortcutsDialog lists the keyboard shortcuts.
func shortcutsDialog(cx *core.Scope, open *core.Signal[bool]) core.Node {
	colors := nswidgets.ColorsOf(cx)
	var rows []core.Node
	for _, s := range shortcutList {
		rows = append(rows, core.View{Style: core.Style{Direction: core.Row, Gap: 16}, Children: []core.Node{
			core.Text{Content: tr(s.title), Style: core.Style{FontSize: 13, FlexGrow: 1, MinWidth: core.Pt(0)}},
			core.Text{Content: keysLabel(s.keys), StyleFunc: func() core.Style { return core.Style{FontSize: 13, Color: colors.Muted(), FlexShrink: 0} }},
		}})
	}
	return ui.Dialog{Open: open.Get, OnOpenChange: func(o bool, _ base.Reason) { open.Set(o) }, TestID: "shortcuts",
		Title: tr("Keyboard shortcuts"),
		Children: []core.Node{core.Scroll{Style: core.Style{MaxHeight: core.Pt(440)}, Children: []core.Node{
			core.View{Style: core.Style{Gap: 12}, Children: rows},
		}}}}
}
