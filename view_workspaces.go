package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func (app *chatApp) workspaceSelector(c *ui.Context) {
	m := app.ensureDesktopWorkspaces()
	current := m.store.Current()
	name := current.Name
	if current.ID == localDesktopWorkspace {
		name = tr("Local")
	}
	button := quiet(c.Key("workspace-switcher")).Height(34).MaxWidth(136).MinWidth(0).PaddingX(8).Gap(6).Radius(6).Label(tr("Switch workspace")).Tooltip(tr("Switch workspace · ") + name)
	button.Children(func() {
		symbol := "device-desktop"
		if current.ID != localDesktopWorkspace {
			symbol = "server"
		}
		icon(c, symbol, 15, muted)
		ui.Text(c, name).Grow(1).MinWidth(0).FontSize(12).SingleLine().Ellipsis("…")
		icon(c, "chevron-down", 11, faint)
	})
	button.PopoverMenuButton(func(menu *ui.Menu) {
		for _, item := range m.store.Items {
			label := item.Name
			if item.ID == localDesktopWorkspace {
				label = tr("Local")
			}
			if menu.Item(label).Radio(item.ID == m.store.Active).Chosen() {
				m.selectWorkspace(item.ID)
			}
		}
		menu.Separator()
		if menu.Item(tr("Add remote workspace…")).Disabled(m.loadErr != nil).Chosen() {
			m.cancelConnection()
			m.open, m.name, m.link, m.editID, m.err = true, "", "", "", ""
		}
		if current.ID != localDesktopWorkspace && menu.Item(tr("Rename workspace…")).Chosen() {
			m.cancelConnection()
			m.open, m.name, m.editID, m.err = true, current.Name, current.ID, ""
		}
	})
}

func (app *chatApp) workspaceConnectionStatus(c *ui.Context) {
	m := app.ensureDesktopWorkspaces()
	if !(m.busy && !m.open) && !(m.err != "" && !m.open) {
		return
	}
	ui.Column(c).FillWidth().Gap(6).Padding(6, 0, 0, 0).Children(func() {
		if m.busy && !m.open {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).FontSize(12)
				ui.Text(c, tr("Connecting to ")+m.pending.Name+"…").Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(11).TextColor(muted)
				if toolButton(c.Key("cancel-workspace"), "x", tr("Cancel connection")).Clicked() {
					m.cancelConnection()
				}
			})
		}
		if m.err != "" && !m.open {
			ui.Text(c, m.err).FontSize(11).TextColor(danger).MaxLines(3)
		}
	})
}

func (m *desktopWorkspaces) dialog(c *ui.Context) {
	wasOpen := m.open
	ui.DialogBase(c.Key("workspace-dialog"), &m.open, func(backdrop, box ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.30))
		box.Width(480).MaxWidthPercent(90).Padding(24).Gap(16).Background(panel).Border(1, border).Radius(14).TextColor(ink)
		title := tr("Add remote workspace")
		if m.editID != "" {
			title = tr("Rename workspace")
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).Grow(1).FontSize(18).FontWeight(600)
			if toolButton(c.Key("close-workspace"), "x", tr("Close workspace dialog")).Clicked() {
				m.cancelConnection()
				m.open = false
			}
		})
		if m.editID == "" {
			ui.Text(c, tr("Once connected, this server's projects and conversations appear in the sidebar.")).FontSize(12).TextColor(muted).LineHeight(1.5)
		}
		ui.Field(c, tr("Workspace name"), func() {
			ui.TextInput(c.Key("workspace-name"), &m.name).Placeholder(tr("For example, prod-us")).Label(tr("Workspace name")).FillWidth().AutoFocus()
		})
		if m.editID == "" {
			ui.Field(c, tr("Connection code"), func() {
				ui.TextInput(c.Key("workspace-link"), &m.link).Placeholder("miya://connect?…").Label(tr("Connection code")).FillWidth().FontSize(12)
			})
		}
		if m.err != "" {
			ui.Text(c, m.err).FontSize(12).TextColor(danger).LineHeight(1.5)
		}
		ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c.Key("workspace-cancel"), tr("Cancel")).Clicked() {
				m.cancelConnection()
				m.open = false
			}
			if m.editID != "" {
				if ui.PrimaryButton(c.Key("workspace-rename"), tr("Save")).Disabled(strings.TrimSpace(m.name) == "").Clicked() {
					m.renameCurrent()
				}
			} else {
				label := tr("Add and connect")
				if m.busy {
					label = tr("Connecting…")
				}
				if ui.PrimaryButton(c.Key("workspace-connect"), label).Disabled(m.busy || strings.TrimSpace(m.link) == "").Clicked() {
					next := m.store.Clone()
					item, err := next.Remember(m.link, m.name)
					if err != nil {
						m.err = err.Error()
					} else {
						m.connectWorkspace(item)
					}
				}
			}
		})
	})
	if wasOpen && !m.open && m.busy {
		m.cancelConnection()
	}
}

func (m *desktopWorkspaces) renameCurrent() {
	next := m.store.Clone()
	for i := range next.Items {
		if next.Items[i].ID == m.editID {
			next.Items[i].Name = strings.TrimSpace(m.name)
		}
	}
	if err := next.Save(); err != nil {
		m.err = err.Error()
		return
	}
	m.store, m.open, m.err = next, false, ""
}
