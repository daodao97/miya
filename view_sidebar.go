package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"miya/internal/model"
)

func (app *chatApp) sidebarView(c *ui.Context) {
	bar := c.TitleBar()
	ui.Column(c).Width(260).Shrink(0).FillHeight().Background(side).Clip().Children(func() {
		ui.Row(c).Height(56).Shrink(0).PaddingX(12).AlignItems(ui.Center).Justify(ui.End).DragWindow().Children(func() {
			if bar.Left > 0 {
				ui.Box(c).Width(bar.Left)
				ui.Spacer(c)
			}
			if toolButton(c.Key("hide-sidebar"), "layout-sidebar", tr("Hide sidebar")).Clicked() {
				app.sidebar = false
			}
		})
		// Only the navigation and project list scroll; the footer is a sibling.
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).PaddingX(12).Gap(3).Children(func() {
				if app.nav(c.Key("nav-new"), "edit", tr("New thread")).Clicked() {
					app.newThread()
				}
				if app.nav(c.Key("nav-automations"), "clock", tr("Automations")).Clicked() {
					app.showAutomations()
				}
			})
			app.sidebarActivity(c.Key("activity"))
			ui.Row(c).AlignItems(ui.Center).Margin(28, 0, 4, 0).Padding(0, 12, 0, 20).Children(func() {
				ui.Text(c, tr("Projects")).FontSize(11).TextColor(faint).Grow(1)
				if toolButton(c.Key("add-project"), "folder-plus", tr("Select project")).Clicked() {
					app.openProjectDialog()
				}
			})
			ui.Column(c).PaddingX(12).Gap(3).Children(func() {
				for _, p := range app.c.projects() {
					app.projectSection(c.Key("project:"+p.Path), p)
				}
			})
		})
		ui.Column(c).Padding(12).Shrink(0).Background(side).BorderWidth(1, 0, 0, 0).BorderColor(line).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(4).Children(func() {
				if toolButton(c.Key("nav-settings"), "settings", tr("Settings")).Height(34).Tooltip(tr("Settings") + " · " + shortcutKeys(ui.Cmd, ",")).Clicked() {
					app.section = "Settings"
				}
				ui.Spacer(c)
				app.workspaceSelector(c.Key("workspace"))
			})
			app.workspaceConnectionStatus(c.Key("workspace-status"))
		})
	})
}

// Activity spans projects, including conversations hidden by the compact list.
func (app *chatApp) sidebarActivity(c *ui.Context) {
	running, completed := app.sidebarActivityThreads()
	for _, group := range []struct {
		label   string
		threads []Thread
	}{{tr("In progress"), running}, {tr("Completed"), completed}} {
		if len(group.threads) == 0 {
			continue
		}
		ui.Column(c.Key(group.label)).Margin(20, 0, 0, 0).PaddingX(12).Gap(2).Children(func() {
			ui.Text(c, group.label).FontSize(11).TextColor(faint).Padding(0, 8, 4, 8)
			for _, t := range group.threads {
				app.threadRow(c.Key(t.ID), t)
			}
		})
	}
}

func (app *chatApp) sidebarActivityThreads() (running, completed []Thread) {
	c := app.c
	seen := map[int]bool{}
	add := func(threads []Thread) {
		for _, t := range threads {
			if seen[t.ID] {
				continue
			}
			seen[t.ID] = true
			if t.Busy && !t.Loading {
				running = append(running, t)
			} else if !t.Busy && !t.Loading && c.desktopThreadUnread(t) {
				completed = append(completed, t)
			}
		}
	}
	add(c.threads())
	for _, p := range c.projects() {
		if p.Path != c.cwd {
			add(c.projectCache[p.Path])
		}
	}
	return displayedThreads(running, true), displayedThreads(completed, true)
}

func (app *chatApp) nav(c *ui.Context, symbol, label string) ui.Element {
	b := quiet(c).Height(34).Gap(10).FontSize(13)
	b.Children(func() {
		icon(c, symbol, 16, muted)
		ui.Text(c, tr(label)).Grow(1).TextColor(ink)
	})
	return b
}

func (app *chatApp) projectSection(c *ui.Context, p Project) {
	ui.Column(c).Gap(2).Children(func() {
		app.projectRow(c, p)
		if p.Path != app.expandedProject {
			return
		}
		threads := app.c.threads()
		ui.Column(c).Padding(0, 0, 0, 20).Gap(2).Children(func() {
			if len(threads) == 0 && app.c.historyStatus() == tr("Loading conversations…") {
				ui.Text(c, tr("Loading conversations…")).TextColor(muted).FontSize(11).Padding(4, 9)
			}
			if len(threads) == 0 && strings.HasPrefix(app.c.historyStatus(), "Unable to load") || strings.HasPrefix(app.c.historyStatus(), "History") {
				ui.Text(c, app.c.historyStatus()).TextColor(muted).FontSize(11).Padding(4, 9)
				if quiet(c.Key("reconnect-history")).Children(func() { ui.Text(c, tr("Reconnect")).TextColor(muted) }).Clicked() {
					app.c.connect()
				}
			}
			expanded := app.threadListExpanded[p.Path]
			for _, t := range displayedThreads(threads, expanded) {
				app.threadRow(c.Key(t.ID), t)
			}
			if len(threads) > collapsedThreadCount {
				label := tr("Expand")
				if expanded {
					label = tr("Collapse")
				}
				if quiet(c.Key("more-threads")).Children(func() { ui.Text(c, label).TextColor(muted).FontSize(12) }).Clicked() {
					app.threadListExpanded[p.Path] = !expanded
				}
			}
		})
	})
}

// projectRow shows a project, with its actions while the pointer is on it.
func (app *chatApp) projectRow(c *ui.Context, p Project) {
	c = c.Key("row")
	wrap := ui.Box(c).FillWidth()
	showActions := wrap.Hovered()
	wrap.ContextMenu(func(m *ui.Menu) { app.projectMenu(m, p) })
	wrap.Children(func() {
		row := quiet(c.Key("open")).FillWidth().Height(34).FontSize(13)
		row.Children(func() {
			icon(c, "folder", 16, faint)
			ui.Text(c, p.Name).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			if !showActions {
				if p.Path != app.expandedProject && app.c.projectBusy(p.Path) {
					ui.Spinner(c).FontSize(13).Label(tr("Project activity: ") + p.Name)
				} else {
					symbol := "chevron-right"
					if p.Path == app.expandedProject {
						symbol = "chevron-down"
					}
					icon(c, symbol, 12, faint)
				}
			}
		})
		if row.Clicked() {
			app.selectProject(p)
		}
		if !showActions {
			return
		}
		ui.Row(c).Absolute().Right(4).Top(4).Gap(2).Padding(1).Background(chosen).Radius(6).Children(func() {
			more := quiet(c.Key("more")).Size(28, 26).PaddingX(4).Justify(ui.Center).Label(tr("Project actions")).Tooltip(tr("Project actions"))
			more.Children(func() { icon(c, "dots", 16, muted) })
			more.PopoverMenuButton(func(m *ui.Menu) { app.projectMenu(m, p) })
			newChat := quiet(c.Key("new")).Size(28, 26).PaddingX(4).Justify(ui.Center).Label(tr("New thread")).Tooltip(tr("New thread"))
			newChat.Children(func() { icon(c, "edit", 16, ink) })
			if newChat.Clicked() {
				app.newThreadIn(p)
			}
		})
	})
}

func (app *chatApp) projectMenu(m *ui.Menu, p Project) {
	// Archiving hides the project and archives its conversations while keeping
	// the Codex project trust configuration intact.
	if m.Item(tr("Archive project")).Chosen() {
		app.c.archiveProject(p.Path)
	}
	m.Separator()
	if m.Item(tr("Delete project")).Chosen() {
		app.c.deleteProject(p.Path)
	}
}

func (app *chatApp) threadMenu(m *ui.Menu, t Thread) {
	if m.Item(tr("Archive conversation")).Chosen() {
		app.c.archiveThread(t)
	}
	if m.Item(tr("Delete conversation")).Chosen() {
		app.c.deleteThread(t)
	}
}

func (app *chatApp) threadRow(c *ui.Context, t Thread) {
	wrap := ui.Box(c).FillWidth()
	hovered := wrap.Hovered()
	wrap.ContextMenu(func(m *ui.Menu) { app.threadMenu(m, t) })
	wrap.Children(func() {
		row := quiet(c.Key("row")).FillWidth().Height(32).FontSize(13)
		if app.c.active() == t.ID {
			row.Background(chosen)
		}
		row.Children(func() {
			agent := model.ThreadAgent(t)
			if t.Automation != "" {
				icon(c, "clock", 14, agentTint(agent))
			} else {
				agentIcon(c, agent, 14)
			}
			ui.Text(c, t.Title).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").TextColor(ink)
			ui.Box(c.Key("status-actions")).Size(24, 24).Shrink(0).Children(func() {
				// Keep the menu anchor built when the pointer leaves the row
				// for its dropdown, while sharing one slot with run status.
				more := quiet(c.Key("more")).Size(24, 24).PaddingX(4).Justify(ui.Center).Label(tr("Conversation actions")).Tooltip(tr("More"))
				more.Children(func() { icon(c, "dots", 16, muted) })
				more.PopoverMenuButton(func(m *ui.Menu) { app.threadMenu(m, t) })
				if !hovered {
					more.Invisible()
					if t.Busy && !t.Loading {
						ui.Row(c.Key("running")).Absolute().Top(0).Right(0).Bottom(0).Left(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
							ui.Spinner(c).FontSize(13).Label(tr("Working…"))
						})
					} else if !t.Busy && app.c.desktopThreadUnread(t) {
						color := ui.Hex("#30a46c")
						if app.dark {
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
			app.selectThread(t)
		}
	})
}

// header is the title bar: the sidebar toggle, the title and the project
// shortcuts. It drags the window, and a double click zooms it.
func (app *chatApp) header(c *ui.Context) {
	bar := c.TitleBar()
	ui.Row(c).Height(56).Shrink(0).AlignItems(ui.Center).Padding(0, 30, 0, 18).Gap(8).DragWindow().Children(func() {
		if !app.sidebar {
			ui.Box(c).Width(max(bar.Left, 52))
			if toolButton(c.Key("show-sidebar"), "layout-sidebar", tr("Show sidebar")).Clicked() {
				app.sidebar = true
			}
		}
		title := strings.Join(strings.Fields(app.c.current().Title), " ")
		if app.section != "" {
			title = tr(app.section)
		}
		ui.Text(c, title).FontSize(12).FontWeight(600).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
		open := quiet(c.Key("open"))
		open.Children(func() {
			icon(c, "terminal-2", 14, ink)
			ui.Text(c, tr("Open"))
			icon(c, "chevron-down", 12, muted)
		})
		if open.Clicked() {
			app.section = ""
			app.openWorkspace()
		}
		if p := app.panel(); !p.visible {
			if toolButton(c.Key("show-panel"), "layout-sidebar-right", tr("Show right panel")).Tooltip(tr("Show right panel · ") + shortcutKeys(ui.Cmd|ui.Alt, "B")).Clicked() {
				app.openPanel(p)
			}
		}
	})
}

func (app *chatApp) sectionView(c *ui.Context) {
	pad := float32(40)
	if app.section == "Settings" {
		pad = 24
	}
	ui.Scroll(c.Key("section:" + app.section)).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(pad).Gap(20).Children(func() {
			if app.section != "Settings" && app.section != "Automations" {
				ui.Text(c, tr(app.section)).FontSize(26).FontWeight(600).SingleLine().Ellipsis("…")
			}
			switch app.section {
			case "Automations":
				app.automationsView(c)
			case "Skills":
				ui.Text(c, tr("Reusable instructions for the way you work.")).TextColor(muted)
			case "Settings":
				app.settingsView(c)
			}
			if app.section != "Automations" && quiet(c.Key("back")).AlignSelf(ui.Start).Children(func() { ui.Text(c, tr("Back to chat")) }).Clicked() {
				app.section = ""
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
