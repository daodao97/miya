package desktopui

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"miya/internal/service/workspaces"
	"miya/internal/widgets"
)

func (app *App) workspaceSelector(c *ui.Context) {
	m := app.EnsureDesktopWorkspaces()
	current := m.Store.Current()
	name := current.Name
	if current.ID == workspaces.Local {
		name = tr("Local")
	}
	button := widgets.Quiet(c.Key("workspace-switcher")).Height(34).MaxWidth(136).MinWidth(0).PaddingX(8).Gap(6).Radius(6).Label(tr("Switch workspace")).Tooltip(tr("Switch workspace · ") + name)
	button.Children(func() {
		symbol := "device-desktop"
		if current.ID != workspaces.Local {
			symbol = "server"
		}
		widgets.Icon(c, symbol, 15, widgets.Muted)
		ui.Text(c, name).Grow(1).MinWidth(0).FontSize(12).SingleLine().Ellipsis("…")
		widgets.Icon(c, "chevron-down", 11, widgets.Faint)
	})
	button.PopoverMenuButton(func(menu *ui.Menu) {
		for _, item := range m.Store.Items {
			label := item.Name
			if item.ID == workspaces.Local {
				label = tr("Local")
			}
			if menu.Item(label).Radio(item.ID == m.Store.Active).Chosen() {
				m.SelectWorkspace(item.ID)
			}
		}
		menu.Separator()
		if menu.Item(tr("Add remote workspace…")).Disabled(m.loadErr != nil).Chosen() {
			m.CancelConnection()
			m.Open, m.name, m.link, m.editID, m.Err = true, "", "", "", ""
		}
		if current.ID != workspaces.Local && menu.Item(tr("Rename workspace…")).Chosen() {
			m.CancelConnection()
			m.Open, m.name, m.editID, m.Err = true, current.Name, current.ID, ""
		}
	})
}

func (app *App) workspaceConnectionStatus(c *ui.Context) {
	m := app.EnsureDesktopWorkspaces()
	if !(m.Busy && !m.Open) && !(m.Err != "" && !m.Open) {
		return
	}
	ui.Column(c).FillWidth().Gap(6).Padding(6, 0, 0, 0).Children(func() {
		if m.Busy && !m.Open {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).FontSize(12)
				ui.Text(c, tr("Connecting to ")+m.pending.Name+"…").Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(11).TextColor(widgets.Muted)
				if widgets.ToolButton(c.Key("cancel-workspace"), "x", tr("Cancel connection")).Clicked() {
					m.CancelConnection()
				}
			})
		}
		if m.Err != "" && !m.Open {
			ui.Text(c, m.Err).FontSize(11).TextColor(widgets.Danger).MaxLines(3)
		}
	})
}

func (m *DesktopWorkspaces) dialog(c *ui.Context) {
	wasOpen := m.Open
	ui.DialogBase(c.Key("workspace-dialog"), &m.Open, func(backdrop, box ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.30))
		box.Width(480).MaxWidthPercent(90).Padding(24).Gap(16).Background(widgets.Panel).Border(1, widgets.Border).Radius(14).TextColor(widgets.Ink)
		title := tr("Add remote workspace")
		if m.editID != "" {
			title = tr("Rename workspace")
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).Grow(1).FontSize(18).FontWeight(600)
			if widgets.ToolButton(c.Key("close-workspace"), "x", tr("Close workspace dialog")).Clicked() {
				m.CancelConnection()
				m.Open = false
			}
		})
		if m.editID == "" {
			ui.Text(c, tr("Once connected, this server's projects and conversations appear in the sidebar.")).FontSize(12).TextColor(widgets.Muted).LineHeight(1.5)
		}
		ui.Field(c, tr("Workspace name"), func() {
			ui.TextInput(c.Key("workspace-name"), &m.name).Placeholder(tr("For example, prod-us")).Label(tr("Workspace name")).FillWidth().AutoFocus()
		})
		if m.editID == "" {
			ui.Field(c, tr("Connection code"), func() {
				ui.TextInput(c.Key("workspace-link"), &m.link).Placeholder("miya://connect?…").Label(tr("Connection code")).FillWidth().FontSize(12)
			})
		}
		if m.Err != "" {
			ui.Text(c, m.Err).FontSize(12).TextColor(widgets.Danger).LineHeight(1.5)
		}
		ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c.Key("workspace-cancel"), tr("Cancel")).Clicked() {
				m.CancelConnection()
				m.Open = false
			}
			if m.editID != "" {
				if ui.PrimaryButton(c.Key("workspace-rename"), tr("Save")).Disabled(strings.TrimSpace(m.name) == "").Clicked() {
					m.renameCurrent()
				}
			} else {
				label := tr("Add and connect")
				if m.Busy {
					label = tr("Connecting…")
				}
				if ui.PrimaryButton(c.Key("workspace-connect"), label).Disabled(m.Busy || strings.TrimSpace(m.link) == "").Clicked() {
					next := m.Store.Clone()
					item, err := next.Remember(m.link, m.name)
					if err != nil {
						m.Err = err.Error()
					} else {
						m.ConnectWorkspace(item)
					}
				}
			}
		})
	})
	if wasOpen && !m.Open && m.Busy {
		m.CancelConnection()
	}
}

func (m *DesktopWorkspaces) renameCurrent() {
	next := m.Store.Clone()
	for i := range next.Items {
		if next.Items[i].ID == m.editID {
			next.Items[i].Name = strings.TrimSpace(m.name)
		}
	}
	if err := next.Save(); err != nil {
		m.Err = err.Error()
		return
	}
	m.Store, m.Open, m.Err = next, false, ""
}
