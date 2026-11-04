package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

type desktopCommandPalette struct {
	open  bool
	query string
}

type desktopPaletteItem struct {
	id, title, detail, icon, keys string
	run                           func()
}

func (item desktopPaletteItem) displayTitle() string {
	if strings.HasPrefix(item.id, "thread:") {
		return item.title
	}
	if strings.HasPrefix(item.id, "workspace:") {
		name := strings.TrimPrefix(item.title, "Switch workspace: ")
		if item.id == "workspace:"+localDesktopWorkspace {
			name = tr("Local")
		}
		return fmt.Sprintf(tr("Switch workspace: %s"), name)
	}
	if strings.HasPrefix(item.id, "project:") {
		return fmt.Sprintf(tr("New thread in %s"), strings.TrimPrefix(item.title, "New thread in "))
	}
	if strings.HasPrefix(item.id, "panel:") {
		return tr(item.title)
	}
	return tr(item.title)
}

func (item desktopPaletteItem) displayDetail() string {
	for _, kind := range []string{"Conversation", "Project"} {
		if suffix, ok := strings.CutPrefix(item.detail, kind+" · "); ok {
			return tr(kind) + " · " + suffix
		}
	}
	return tr(item.detail)
}

func (app *chatApp) openCommandPalette() {
	app.closeMenus()
	app.focusComposer = false
	app.commandPalette = desktopCommandPalette{open: true}
}

// Match words independently, with ordered letters within each word, so both
// "open settings" and abbreviations such as "stng" find the same action.
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

func (app *chatApp) commandPaletteItems() []desktopPaletteItem {
	var items []desktopPaletteItem
	add := func(item desktopPaletteItem) {
		if paletteMatches(item.title+" "+item.detail, app.commandPalette.query) || paletteMatches(item.displayTitle()+" "+item.displayDetail(), app.commandPalette.query) {
			items = append(items, item)
		}
	}
	for _, binding := range app.desktopShortcuts() {
		if binding.mods == ui.Cmd|ui.Shift && binding.key == ui.KeyP {
			continue
		}
		if binding.key == ui.KeyPeriod && (!app.c.current().Busy || app.c.current().StopRequested || app.section != "") {
			continue
		}
		if binding.key == ui.KeyO && (app.section != "" || app.c.current().ReadOnly || app.c.current().Loading) {
			continue
		}
		add(desktopPaletteItem{
			id:    fmt.Sprintf("shortcut:%d:%d", binding.mods, binding.key),
			title: binding.title, detail: "Command", icon: "chevron-right",
			keys: shortcutKeys(binding.mods, binding.name), run: binding.run,
		})
	}
	add(desktopPaletteItem{id: "automations", title: "Open automations", detail: "Command", icon: "clock", run: app.showAutomations})
	add(desktopPaletteItem{id: "skills", title: "Open skills", detail: "Command", icon: "stack-2", run: func() { app.section = "Skills" }})
	add(desktopPaletteItem{id: "add-project", title: "Add project…", detail: "Command", icon: "folder-plus", run: app.openProjectDialog})
	for _, kind := range []surfaceKind{surfaceFiles, surfaceDiff, surfaceBrowser, surfaceRuntime} {
		title := map[surfaceKind]string{surfaceFiles: "Open files", surfaceDiff: "Open review", surfaceBrowser: "Open browser", surfaceRuntime: "Open background tasks"}[kind]
		add(desktopPaletteItem{id: fmt.Sprintf("panel:%d", kind), title: title, detail: "Panel", icon: kind.icon(), run: func() {
			app.openSurface(app.panel(), kind)
		}})
	}
	hub := app.ensureDesktopWorkspaces()
	if hub.loadErr == nil {
		add(desktopPaletteItem{id: "add-workspace", title: "Add remote workspace…", detail: "Workspace", icon: "server", run: func() {
			hub.cancelConnection()
			hub.open, hub.name, hub.link, hub.editID, hub.err = true, "", "", "", ""
		}})
	}
	for _, workspace := range hub.store.Items {
		if workspace.ID == hub.store.Active {
			continue
		}
		add(desktopPaletteItem{id: "workspace:" + workspace.ID, title: "Switch workspace: " + workspace.Name, detail: "Workspace", icon: "device-desktop", run: func() {
			hub.selectWorkspace(workspace.ID)
		}})
	}
	for _, project := range app.c.projects() {
		add(desktopPaletteItem{id: "project:" + project.Path, title: "New thread in " + project.Name, detail: "Project · " + project.Path, icon: "folder", run: func() { app.newThreadIn(project) }})
	}
	for _, choice := range app.switcherChoices("") {
		add(desktopPaletteItem{id: fmt.Sprintf("thread:%d", choice.ID), title: threadRefTitle(choice.Thread), detail: "Conversation · " + choice.ProjectName + " · " + backendOf(choice.Thread).Name(), icon: "message-circle", run: func() { app.selectThread(choice.Thread) }})
	}
	return items
}

func (app *chatApp) commandPaletteView(c *ui.Context) {
	p := &app.commandPalette
	if !p.open {
		return
	}
	ran := false
	ui.DialogBase(c.Key("command-palette"), &p.open, func(backdrop, box ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.30)).Justify(ui.Start).Padding(78, 0, 0, 0)
		box.Width(640).MaxWidthPercent(90).MaxHeightPercent(80).Background(panel).Border(1, border).Radius(14).Clip().TextColor(ink)
		var choices ui.ComboboxParts
		ui.Row(c).FillWidth().Padding(16, 20).Gap(12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(line).Children(func() {
			icon(c, "search", 18, muted)
			choices = ui.ComboboxBase(c.Key("command-query"), &p.query)
			choices.Input.Grow(1).MinWidth(0).FontSize(15).Label(tr("Search commands")).Placeholder(tr("Search commands, conversations, or workspaces…")).AutoFocus()
			ui.Text(c, "Esc").FontSize(11).TextColor(faint)
		})
		if choices.Input.Shortcut(ui.Cmd|ui.Shift, ui.KeyP) {
			p.open = false
		}
		var items []desktopPaletteItem
		choices.Inline(func(list ui.Element) {
			choices.Input.Changed()
			items = app.commandPaletteItems()
			list.FillWidth()
			ui.Scroll(c.Key("command-options")).Height(float32(min(max(len(items), 1)*42+12, 336))).Padding(6).Children(func() {
				if len(items) == 0 {
					ui.Text(c, tr("No matching commands or conversations")).FontSize(13).TextColor(muted).Padding(14)
				}
				for _, item := range items {
					title, detail := item.displayTitle(), item.displayDetail()
					row := choices.Item(item.id).Label(title).Tooltip(title + " · " + detail).Height(42).PaddingX(12).Gap(10).Radius(7).AlignItems(ui.Center).Cursor(ui.CursorPointer)
					if row.Highlighted() {
						row.Background(chosen)
					} else if row.Hovered() {
						row.Background(hover)
					}
					row.Children(func() {
						icon(c, item.icon, 16, muted)
						ui.Text(c, title).FontSize(13).FontWeight(500).MinWidth(0).SingleLine().Ellipsis("…")
						ui.Text(c, detail).FontSize(11).TextColor(faint).MinWidth(0).SingleLine().Ellipsis("…")
						ui.Spacer(c)
						if item.keys != "" {
							ui.Text(c, item.keys).FontSize(11).TextColor(muted).Shrink(0)
						}
					})
				}
			})
		})
		if id, ok := choices.Chosen(); ok {
			for _, item := range items {
				if item.id == id {
					p.open = false
					ran = true
					item.run()
					break
				}
			}
		}
		ui.Text(c, tr("↑ ↓ Navigate · Enter Run")).FontSize(11).TextColor(muted).Padding(10, 20).BorderWidth(1, 0, 0, 0).BorderColor(line)
	})
	if !p.open && !ran && app.section == "" {
		app.focusComposer = true
	}
}
