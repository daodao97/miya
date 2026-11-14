package nsui

import (
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/chat/model"
	"miya/internal/nswidgets"
)

// paletteEntry is a command palette row and what choosing it does.
type paletteEntry struct {
	item base.CommandItem
	run  func()
}

// paletteEntries are the commands, then the conversations in the sidebar's
// order. Stop shows only while the current turn runs.
func paletteEntries(cmd commands, current model.Thread, threads []model.Thread, selectThread func(model.Thread), now time.Time) []paletteEntry {
	command := tr("Command")
	entries := []paletteEntry{
		{base.CommandItem{ID: "cmd:new", Label: tr("New conversation"), Group: command, Shortcut: "mod+n", Icon: nswidgets.SVG("edit")}, cmd.newThread},
		{base.CommandItem{ID: "cmd:settings", Label: tr("Open settings"), Group: command, Shortcut: "mod+,", Icon: nswidgets.SVG("settings")}, cmd.openSettings},
		{base.CommandItem{ID: "cmd:sidebar", Label: tr("Toggle sidebar"), Group: command, Shortcut: "mod+b", Icon: nswidgets.SVG("layout-sidebar")}, cmd.toggleSidebar},
	}
	if current.Busy && !current.StopRequested {
		entries = append(entries, paletteEntry{base.CommandItem{ID: "cmd:stop", Label: tr("Stop response"), Group: command, Shortcut: "mod+.", Icon: nswidgets.SVG("player-pause")}, cmd.stop})
	}
	conversations := tr("Conversations")
	for _, g := range threadGroups(threads, now) {
		for _, t := range g.threads {
			detail := t.Project
			if name := model.AgentName(model.ThreadAgent(t)); name != "" {
				if detail != "" {
					detail += " · "
				}
				detail += name
			}
			entries = append(entries, paletteEntry{
				base.CommandItem{ID: "thread:" + strconv.Itoa(t.ID), Label: threadTitle(t), Description: detail, Group: conversations, Icon: nswidgets.SVG("message-circle")},
				func() { selectThread(t) },
			})
		}
	}
	return entries
}

// paletteMatches matches words independently, with ordered letters within
// each word, so both "open settings" and abbreviations such as "stng" find
// the same action, as the MyGo palette did.
func paletteMatches(text, query string) bool {
	text = strings.ToLower(text)
	for _, word := range strings.Fields(strings.ToLower(query)) {
		letters := []rune(word)
		i := 0
		for _, r := range text {
			if i < len(letters) && r == letters[i] {
				i++
			}
		}
		if i != len(letters) {
			return false
		}
	}
	return true
}

// commandPalette is the ⌘K dialog over the window.
func commandPalette(cx *core.Scope, open *core.Signal[bool], entries func() []paletteEntry) core.Node {
	// The entries the dialog shows, by ID, for running what it reports.
	var shown map[string]func()
	items := func() []base.CommandItem {
		list := entries()
		shown = make(map[string]func(), len(list))
		out := make([]base.CommandItem, len(list))
		for i, e := range list {
			shown[e.item.ID] = e.run
			out[i] = e.item
		}
		return out
	}
	return ui.CommandDialog{TestID: "command-palette", Open: open.Get,
		OnOpenChange: func(o bool, _ base.Reason) { open.Set(o) },
		Command: ui.Command{Items: items, AutoFocus: true,
			Placeholder: tr("Search commands, conversations, or workspaces…"), SearchLabel: tr("Search commands"), Label: tr("Search commands"),
			Empty: tr("No matching commands or conversations"),
			Filter: func(items []base.CommandItem, query string) []base.CommandItem {
				var out []base.CommandItem
				for _, it := range items {
					if paletteMatches(it.Label+" "+it.Description+" "+it.Group, query) {
						out = append(out, it)
					}
				}
				return out
			},
			OnSelect: func(id string) {
				open.Set(false)
				if run := shown[id]; run != nil {
					run()
				}
			}}}
}
