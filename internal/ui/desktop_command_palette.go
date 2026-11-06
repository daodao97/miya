package desktopui

import (
	fmt "fmt"
	appmodel "miya/internal/appmodel"
	model "miya/internal/model"
	widgets "miya/internal/widgets"
	strings "strings"

	ui "github.com/egoist/mygo/ui"
)

type desktopCommandPalette struct {
	Open  bool
	Query string
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
		if item.id == "workspace:"+appmodel.LocalDesktopWorkspace {
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

func (app *App) openCommandPalette() {
	app.closeMenus()
	app.focusComposer = false
	app.CommandPalette = desktopCommandPalette{Open: true}
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

func (app *App) commandPaletteItems() []desktopPaletteItem {
	var items []desktopPaletteItem
	add := func(item desktopPaletteItem) {
		if paletteMatches(item.title+" "+item.detail, app.CommandPalette.Query) || paletteMatches(item.displayTitle()+" "+item.displayDetail(), app.CommandPalette.Query) {
			items = append(items, item)
		}
	}
	for _, binding := range app.desktopShortcuts() {
		if binding.mods == ui.Cmd|ui.Shift && binding.key == ui.KeyP {
			continue
		}
		if binding.key == ui.KeyPeriod && (!app.C.Current().Busy || app.C.Current().StopRequested || app.Section != "") {
			continue
		}
		if binding.key == ui.KeyO && (app.Section != "" || app.C.Current().ReadOnly || app.C.Current().Loading) {
			continue
		}
		add(desktopPaletteItem{
			id:    fmt.Sprintf("shortcut:%d:%d", binding.mods, binding.key),
			title: binding.title, detail: "Command", icon: "chevron-right",
			keys: ShortcutKeys(binding.mods, binding.name), run: binding.run,
		})
	}
	add(desktopPaletteItem{id: "automations", title: "Open automations", detail: "Command", icon: "clock", run: app.ShowAutomations})
	add(desktopPaletteItem{id: "skills", title: "Open skills", detail: "Command", icon: "stack-2", run: func() { app.Section = "Skills" }})
	add(desktopPaletteItem{id: "add-project", title: "Add project…", detail: "Command", icon: "folder-plus", run: app.OpenProjectDialog})
	for _, kind := range []surfaceKind{SurfaceFiles, surfaceDiff, surfaceBrowser, SurfaceRuntime} {
		title := map[surfaceKind]string{SurfaceFiles: "Open files", surfaceDiff: "Open review", surfaceBrowser: "Open browser", SurfaceRuntime: "Open background tasks"}[kind]
		add(desktopPaletteItem{id: fmt.Sprintf("panel:%d", kind), title: title, detail: "Panel", icon: kind.icon(), run: func() {
			app.openSurface(app.Panel(), kind)
		}})
	}
	hub := app.EnsureDesktopWorkspaces()
	if hub.loadErr == nil {
		add(desktopPaletteItem{id: "add-workspace", title: "Add remote workspace…", detail: "Workspace", icon: "server", run: func() {
			hub.CancelConnection()
			hub.Open, hub.name, hub.link, hub.editID, hub.Err = true, "", "", "", ""
		}})
	}
	for _, workspace := range hub.Store.Items {
		if workspace.ID == hub.Store.Active {
			continue
		}
		add(desktopPaletteItem{id: "workspace:" + workspace.ID, title: "Switch workspace: " + workspace.Name, detail: "Workspace", icon: "device-desktop", run: func() {
			hub.SelectWorkspace(workspace.ID)
		}})
	}
	for _, project := range app.C.State().Projects() {
		add(desktopPaletteItem{id: "project:" + project.Path, title: "New thread in " + project.Name, detail: "Project · " + project.Path, icon: "folder", run: func() { app.newThreadIn(project) }})
	}
	for _, choice := range app.SwitcherChoices("") {
		add(desktopPaletteItem{id: fmt.Sprintf("thread:%d", choice.ID), title: appmodel.ThreadRefTitle(choice.Thread), detail: "Conversation · " + choice.ProjectName + " · " + model.AgentName(model.ThreadAgent(choice.Thread)), icon: "message-circle", run: func() { app.SelectThread(choice.Thread) }})
	}
	return items
}

func (app *App) commandPaletteView(c *ui.Context) {
	p := &app.CommandPalette
	if !p.Open {
		return
	}
	ran := false
	ui.DialogBase(c.Key("command-palette"), &p.Open, func(backdrop, box ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.30)).Justify(ui.Start).Padding(78, 0, 0, 0)
		box.Width(640).MaxWidthPercent(90).MaxHeightPercent(80).Background(widgets.Panel).Border(1, widgets.Border).Radius(14).Clip().TextColor(widgets.Ink)
		var choices ui.ComboboxParts
		ui.Row(c).FillWidth().Padding(16, 20).Gap(12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(widgets.Line).Children(func() {
			widgets.Icon(c, "search", 18, widgets.Muted)
			choices = ui.ComboboxBase(c.Key("command-query"), &p.Query)
			choices.Input.Grow(1).MinWidth(0).FontSize(15).Label(tr("Search commands")).Placeholder(tr("Search commands, conversations, or workspaces…")).AutoFocus()
			ui.Text(c, "Esc").FontSize(11).TextColor(widgets.Faint)
		})
		if choices.Input.Shortcut(ui.Cmd|ui.Shift, ui.KeyP) {
			p.Open = false
		}
		var items []desktopPaletteItem
		choices.Inline(func(list ui.Element) {
			choices.Input.Changed()
			items = app.commandPaletteItems()
			list.FillWidth()
			ui.Scroll(c.Key("command-options")).Height(float32(min(max(len(items), 1)*42+12, 336))).Padding(6).Children(func() {
				if len(items) == 0 {
					ui.Text(c, tr("No matching commands or conversations")).FontSize(13).TextColor(widgets.Muted).Padding(14)
				}
				for _, item := range items {
					title, detail := item.displayTitle(), item.displayDetail()
					row := choices.Item(item.id).Label(title).Tooltip(title + " · " + detail).Height(42).PaddingX(12).Gap(10).Radius(7).AlignItems(ui.Center).Cursor(ui.CursorPointer)
					if row.Highlighted() {
						row.Background(widgets.Chosen)
					} else if row.Hovered() {
						row.Background(widgets.Hover)
					}
					row.Children(func() {
						widgets.Icon(c, item.icon, 16, widgets.Muted)
						ui.Text(c, title).FontSize(13).FontWeight(500).MinWidth(0).SingleLine().Ellipsis("…")
						ui.Text(c, detail).FontSize(11).TextColor(widgets.Faint).MinWidth(0).SingleLine().Ellipsis("…")
						ui.Spacer(c)
						if item.keys != "" {
							ui.Text(c, item.keys).FontSize(11).TextColor(widgets.Muted).Shrink(0)
						}
					})
				}
			})
		})
		if id, ok := choices.Chosen(); ok {
			for _, item := range items {
				if item.id == id {
					p.Open = false
					ran = true
					item.run()
					break
				}
			}
		}
		ui.Text(c, tr("↑ ↓ Navigate · Enter Run")).FontSize(11).TextColor(widgets.Muted).Padding(10, 20).BorderWidth(1, 0, 0, 0).BorderColor(widgets.Line)
	})
	if !p.Open && !ran && app.Section == "" {
		app.focusComposer = true
	}
}
