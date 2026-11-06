package desktopui

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	projectmodel "miya/internal/project/model"
	"miya/internal/widgets"
)

func (app *App) SidebarView(c *ui.Context) {
	bar := c.TitleBar()
	ui.Column(c).Width(260).Shrink(0).FillHeight().Background(widgets.Side).Clip().Children(func() {
		ui.Row(c).Height(56).Shrink(0).PaddingX(12).AlignItems(ui.Center).Justify(ui.End).DragWindow().Children(func() {
			if bar.Left > 0 {
				ui.Box(c).Width(bar.Left)
				ui.Spacer(c)
			}
			if widgets.ToolButton(c.Key("hide-sidebar"), "layout-sidebar", tr("Hide sidebar")).Clicked() {
				app.Sidebar = false
			}
		})
		// Only the navigation and project list scroll; the footer is a sibling.
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).PaddingX(12).Gap(3).Children(func() {
				if app.nav(c.Key("nav-new"), "edit", tr("New thread")).Clicked() {
					app.newThread()
				}
				if app.nav(c.Key("nav-automations"), "clock", tr("Automations")).Clicked() {
					app.ShowAutomations()
				}
			})
			app.sidebarActivity(c.Key("activity"))
			ui.Row(c).AlignItems(ui.Center).Margin(28, 0, 4, 0).Padding(0, 12, 0, 20).Children(func() {
				ui.Text(c, tr("Projects")).FontSize(11).TextColor(widgets.Faint).Grow(1)
				if widgets.ToolButton(c.Key("add-project"), "folder-plus", tr("Select project")).Clicked() {
					app.OpenProjectDialog()
				}
			})
			ui.Column(c).PaddingX(12).Gap(3).Children(func() {
				for _, p := range app.C.State().Projects() {
					app.projectSection(c.Key("project:"+p.Path), p)
				}
			})
		})
		ui.Column(c).Padding(12).Shrink(0).Background(widgets.Side).BorderWidth(1, 0, 0, 0).BorderColor(widgets.Line).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(4).Children(func() {
				if widgets.ToolButton(c.Key("nav-settings"), "settings", tr("Settings")).Height(34).Tooltip(tr("Settings") + " · " + ShortcutKeys(ui.Cmd, ",")).Clicked() {
					app.Section = "Settings"
				}
				ui.Spacer(c)
				app.workspaceSelector(c.Key("workspace"))
			})
			app.workspaceConnectionStatus(c.Key("workspace-status"))
		})
	})
}

// Activity spans projects, including conversations hidden by the compact list.
func (app *App) sidebarActivity(c *ui.Context) {
	running, completed := app.SidebarActivityThreads()
	for _, group := range []struct {
		label   string
		threads []model.Thread
	}{{tr("In progress"), running}, {tr("Completed"), completed}} {
		if len(group.threads) == 0 {
			continue
		}
		ui.Column(c.Key(group.label)).Margin(20, 0, 0, 0).PaddingX(12).Gap(2).Children(func() {
			ui.Text(c, group.label).FontSize(11).TextColor(widgets.Faint).Padding(0, 8, 4, 8)
			for _, t := range group.threads {
				app.threadRow(c.Key(t.ID), t)
			}
		})
	}
}

func (app *App) SidebarActivityThreads() (running, completed []model.Thread) {
	c := app.C
	seen := map[int]bool{}
	add := func(threads []model.Thread) {
		for _, t := range threads {
			if seen[t.ID] {
				continue
			}
			seen[t.ID] = true
			if t.Busy && !t.Loading {
				running = append(running, t)
			} else if !t.Busy && !t.Loading && c.DesktopThreadUnread(t) {
				completed = append(completed, t)
			}
		}
	}
	add(c.State().Threads())
	for _, p := range c.State().Projects() {
		if p.Path != c.State().Cwd {
			add(c.State().ProjectCache[p.Path])
		}
	}
	return msgfmt.DisplayedThreads(running, true), msgfmt.DisplayedThreads(completed, true)
}

func (app *App) nav(c *ui.Context, symbol, label string) ui.Element {
	b := widgets.Quiet(c).Height(34).Gap(10).FontSize(13)
	b.Children(func() {
		widgets.Icon(c, symbol, 16, widgets.Muted)
		ui.Text(c, tr(label)).Grow(1).TextColor(widgets.Ink)
	})
	return b
}

func (app *App) projectSection(c *ui.Context, p projectmodel.Project) {
	ui.Column(c).Gap(2).Children(func() {
		app.projectRow(c, p)
		if p.Path != app.ExpandedProject {
			return
		}
		threads := app.C.State().Threads()
		ui.Column(c).Padding(0, 0, 0, 20).Gap(2).Children(func() {
			if len(threads) == 0 && app.C.State().HistoryStatus() == tr("Loading conversations…") {
				ui.Text(c, tr("Loading conversations…")).TextColor(widgets.Muted).FontSize(11).Padding(4, 9)
			}
			if len(threads) == 0 && strings.HasPrefix(app.C.State().HistoryStatus(), "Unable to load") || strings.HasPrefix(app.C.State().HistoryStatus(), "History") {
				ui.Text(c, app.C.State().HistoryStatus()).TextColor(widgets.Muted).FontSize(11).Padding(4, 9)
				if widgets.Quiet(c.Key("reconnect-history")).Children(func() { ui.Text(c, tr("Reconnect")).TextColor(widgets.Muted) }).Clicked() {
					app.C.Connect()
				}
			}
			expanded := app.threadListExpanded[p.Path]
			for _, t := range msgfmt.DisplayedThreads(threads, expanded) {
				app.threadRow(c.Key(t.ID), t)
			}
			if len(threads) > msgfmt.CollapsedThreadCount {
				label := tr("Expand")
				if expanded {
					label = tr("Collapse")
				}
				if widgets.Quiet(c.Key("more-threads")).Children(func() { ui.Text(c, label).TextColor(widgets.Muted).FontSize(12) }).Clicked() {
					app.threadListExpanded[p.Path] = !expanded
				}
			}
		})
	})
}

// projectRow shows a project, with its actions while the pointer is on it.
func (app *App) projectRow(c *ui.Context, p projectmodel.Project) {
	c = c.Key("row")
	wrap := ui.Box(c).FillWidth()
	showActions := wrap.Hovered()
	wrap.ContextMenu(func(m *ui.Menu) { app.projectMenu(m, p) })
	wrap.Children(func() {
		row := widgets.Quiet(c.Key("open")).FillWidth().Height(34).FontSize(13)
		row.Children(func() {
			widgets.Icon(c, "folder", 16, widgets.Faint)
			ui.Text(c, p.Name).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			if !showActions {
				if p.Path != app.ExpandedProject && app.C.ProjectBusy(p.Path) {
					ui.Spinner(c).FontSize(13).Label(tr("Project activity: ") + p.Name)
				} else {
					symbol := "chevron-right"
					if p.Path == app.ExpandedProject {
						symbol = "chevron-down"
					}
					widgets.Icon(c, symbol, 12, widgets.Faint)
				}
			}
		})
		if row.Clicked() {
			app.selectProject(p)
		}
		if !showActions {
			return
		}
		ui.Row(c).Absolute().Right(4).Top(4).Gap(2).Padding(1).Background(widgets.Chosen).Radius(6).Children(func() {
			more := widgets.Quiet(c.Key("more")).Size(28, 26).PaddingX(4).Justify(ui.Center).Label(tr("Project actions")).Tooltip(tr("Project actions"))
			more.Children(func() { widgets.Icon(c, "dots", 16, widgets.Muted) })
			more.PopoverMenuButton(func(m *ui.Menu) { app.projectMenu(m, p) })
			newChat := widgets.Quiet(c.Key("new")).Size(28, 26).PaddingX(4).Justify(ui.Center).Label(tr("New thread")).Tooltip(tr("New thread"))
			newChat.Children(func() { widgets.Icon(c, "edit", 16, widgets.Ink) })
			if newChat.Clicked() {
				app.newThreadIn(p)
			}
		})
	})
}

func (app *App) projectMenu(m *ui.Menu, p projectmodel.Project) {
	// Archiving hides the project and archives its conversations while keeping
	// the Codex project trust configuration intact.
	if m.Item(tr("Archive project")).Chosen() {
		app.C.ArchiveProject(p.Path)
	}
	m.Separator()
	if m.Item(tr("Delete project")).Chosen() {
		app.C.DeleteProject(p.Path)
	}
}

func (app *App) threadMenu(m *ui.Menu, t model.Thread) {
	if m.Item(tr("Archive conversation")).Chosen() {
		app.C.ArchiveThread(t)
	}
	if m.Item(tr("Delete conversation")).Chosen() {
		app.C.DeleteThread(t)
	}
}

func (app *App) threadRow(c *ui.Context, t model.Thread) {
	wrap := ui.Box(c).FillWidth()
	hovered := wrap.Hovered()
	wrap.ContextMenu(func(m *ui.Menu) { app.threadMenu(m, t) })
	wrap.Children(func() {
		row := widgets.Quiet(c.Key("row")).FillWidth().Height(32).FontSize(13)
		if app.C.State().Active() == t.ID {
			row.Background(widgets.Chosen)
		}
		row.Children(func() {
			agent := model.ThreadAgent(t)
			if t.Automation != "" {
				widgets.Icon(c, "clock", 14, agentTint(agent))
			} else {
				widgets.AgentIcon(c, agent, 14)
			}
			ui.Text(c, t.Title).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").TextColor(widgets.Ink)
			ui.Box(c.Key("status-actions")).Size(24, 24).Shrink(0).Children(func() {
				// Keep the menu anchor built when the pointer leaves the row
				// for its dropdown, while sharing one slot with run status.
				more := widgets.Quiet(c.Key("more")).Size(24, 24).PaddingX(4).Justify(ui.Center).Label(tr("Conversation actions")).Tooltip(tr("More"))
				more.Children(func() { widgets.Icon(c, "dots", 16, widgets.Muted) })
				more.PopoverMenuButton(func(m *ui.Menu) { app.threadMenu(m, t) })
				if !hovered {
					more.Invisible()
					if t.Busy && !t.Loading {
						ui.Row(c.Key("running")).Absolute().Top(0).Right(0).Bottom(0).Left(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
							ui.Spinner(c).FontSize(13).Label(tr("Working…"))
						})
					} else if !t.Busy && app.C.DesktopThreadUnread(t) {
						color := ui.Hex("#30a46c")
						if app.Dark {
							color = ui.Hex("#3dd68c")
						}
						ui.Row(c.Key("unread")).Absolute().Top(0).Right(0).Bottom(0).Left(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
							ui.Box(c).Size(8, 8).Radius(4).Background(color).Label(tr("Unread: ") + t.Title)
						})
					}
				}
			})
		})
		if row.Clicked() {
			app.SelectThread(t)
		}
	})
}

// header is the title bar: the sidebar toggle, the title and the project
// shortcuts. It drags the window, and a double click zooms it.
func (app *App) header(c *ui.Context) {
	bar := c.TitleBar()
	ui.Row(c).Height(56).Shrink(0).AlignItems(ui.Center).Padding(0, 30, 0, 18).Gap(8).DragWindow().Children(func() {
		if !app.Sidebar {
			ui.Box(c).Width(max(bar.Left, 52))
			if widgets.ToolButton(c.Key("show-sidebar"), "layout-sidebar", tr("Show sidebar")).Clicked() {
				app.Sidebar = true
			}
		}
		title := strings.Join(strings.Fields(app.C.Current().Title), " ")
		if app.Section != "" {
			title = tr(app.Section)
		}
		ui.Text(c, title).FontSize(12).FontWeight(600).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
		open := widgets.Quiet(c.Key("open"))
		open.Children(func() {
			widgets.Icon(c, "terminal-2", 14, widgets.Ink)
			ui.Text(c, tr("Open"))
			widgets.Icon(c, "chevron-down", 12, widgets.Muted)
		})
		if open.Clicked() {
			app.Section = ""
			app.openWorkspace()
		}
		if p := app.Panel(); !p.Visible {
			if widgets.ToolButton(c.Key("show-panel"), "layout-sidebar-right", tr("Show right panel")).Tooltip(tr("Show right panel · ") + ShortcutKeys(ui.Cmd|ui.Alt, "B")).Clicked() {
				app.openPanel(p)
			}
		}
	})
}

func (app *App) sectionView(c *ui.Context) {
	pad := float32(40)
	if app.Section == "Settings" {
		pad = 24
	}
	ui.Scroll(c.Key("section:" + app.Section)).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(pad).Gap(20).Children(func() {
			if app.Section != "Settings" && app.Section != "Automations" {
				ui.Text(c, tr(app.Section)).FontSize(26).FontWeight(600).SingleLine().Ellipsis("…")
			}
			switch app.Section {
			case "Automations":
				app.automationsView(c)
			case "Skills":
				ui.Text(c, tr("Reusable instructions for the way you work.")).TextColor(widgets.Muted)
			case "Settings":
				app.settingsView(c)
			}
			if app.Section != "Automations" && widgets.Quiet(c.Key("back")).AlignSelf(ui.Start).Children(func() { ui.Text(c, tr("Back to chat")) }).Clicked() {
				app.Section = ""
			}
		})
	})
}

func menuTitle(key string) string {
	return tr(map[string]string{"model": "Select model", "effort": "Reasoning effort", "project": "Select project", "open": "Open project", "details": "Project details", "context": "Add context", "permissions": "Permissions", "branch": "Current branch"}[key])
}

func menuDescription(key, project string) string {
	switch key {
	case "details":
		return tr("Project: ") + project + tr("\nEnvironment: local Codex app-server")
	case "context":
		return tr("Attach files with + or paste an image/file into the composer.")
	case "permissions":
		return tr("Workspace write · On-request approvals\nCodex may read and edit files in the workspace. Requests for additional access appear for approval.")
	case "branch":
		return tr("Ask Codex to inspect the current branch. The branch picker is not connected yet.")
	default:
		return tr("Project: ") + project + tr("\nThe editor shortcut is not connected yet.")
	}
}
