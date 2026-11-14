package nsui

import (
	"runtime"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"

	"miya/internal/chat/model"
)

// commands are what the menu bar and the window's shortcuts do.
type commands struct {
	newThread, openSettings, toggleSidebar, stop, openPalette func()
	// step selects the conversation step places after the current one in
	// the sidebar's order, before it when negative.
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
	cx.OnKey("mod+b", cmd.toggleSidebar)
	cx.OnKey("mod+.", cmd.stop)
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
		}},
		{Label: tr("Window"), Items: []desktop.MenuItem{
			{Role: desktop.MenuMinimize}, {Role: desktop.MenuZoom},
		}},
	}
}

// stepThread is the ID of the conversation step places from current in the
// sidebar's order, wrapping around; current itself when there is none
// other.
func stepThread(threads []model.Thread, current, step int, now time.Time) int {
	var order []int
	for _, g := range threadGroups(threads, now) {
		for _, t := range g.threads {
			order = append(order, t.ID)
		}
	}
	if len(order) == 0 {
		return current
	}
	at := -1
	for i, id := range order {
		if id == current {
			at = i
			break
		}
	}
	if at < 0 {
		if step < 0 {
			return order[len(order)-1]
		}
		return order[0]
	}
	n := len(order)
	return order[((at+step)%n+n)%n]
}
