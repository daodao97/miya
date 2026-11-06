package mobile

import (
	textutil "miya/internal/textutil"
	widgets "miya/internal/widgets"

	ui "github.com/egoist/mygo/ui"
)

func (m *App) openDesktopManager() {
	m.OpenDrawerPage("/desktops")
}

func (m *App) addDesktop() {
	m.Paste, m.err = "", ""
	m.Router().Push("/desktop/add")
}

func (m *App) drawerDesktopSwitcher(c *ui.Context) {
	p := m.Pal
	b := m.phoneButton(c.Key("desktop-switcher")).Column().Grow(1).MinWidth(0).Padding(0).Gap(4).AlignItems(ui.Start).Label(tr("Switch desktop"))
	b.Children(func() {
		ui.Text(c, "Miya").FontSize(m.Font(22)).FontWeight(600).SingleLine()
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			label := m.ConnectionLine()
			if label == "" {
				label = textutil.FirstNonEmpty(m.Host, tr("Connected"))
			}
			ui.Text(c, label).FontSize(m.Font(12)).TextColor(p.muted).MinWidth(0).Shrink(1).SingleLine().Ellipsis("…")
			widgets.Icon(c, "chevron-down", 11, p.muted)
		})
	})
	if b.Clicked() {
		m.openDesktopManager()
	}
}

func (m *App) desktopsView(c *ui.Context) {
	p := m.Pal
	ui.Row(c).FillWidth().MinHeight(64).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		m.navigationBack(c, "/desktops")
		ui.Text(c, tr("Desktop")).Grow(1).TextAlign(ui.Center).FontSize(m.Font(16)).FontWeight(600)
		if m.homeAction(c, "add-desktop", tr("Add desktop"), "plus").Clicked() {
			m.addDesktop()
		}
	})
	ui.Scroll(c.Key("saved-desktops")).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(20, 16).Gap(14).Children(func() {
			ui.Text(c, tr("Saved desktops")).FontSize(m.Font(13)).TextColor(p.muted).Margin(0, 8)
			if m.DesktopNotice != "" {
				ui.Text(c, m.DesktopNotice).FontSize(m.Font(14)).TextColor(p.muted).LineHeight(1.4)
			}
			if m.desktopError != "" {
				ui.Text(c, m.desktopError).FontSize(m.Font(14)).TextColor(widgets.Danger)
				if m.pill(c, "retry-save", tr("Retry"), false).Clicked() {
					if m.desktopLoadFailed {
						go m.loadSaved()
					} else {
						m.SaveDesktops()
					}
				}
			}
			for _, desktop := range m.Desktops.Items {
				ui.Row(c.Key(desktop.ID)).FillWidth().Padding(8).Gap(4).AlignItems(ui.Center).Background(p.surface).Radius(16).Children(func() {
					current := desktop.Link == m.Link
					row := m.phoneButton(c.Key("switch")).Grow(1).MinWidth(0).MinHeight(60).Padding(6, 8).Gap(12).Label(tr("Connect to desktop ") + desktop.Name)
					row.Children(func() {
						widgets.Icon(c, "terminal-2", 23, p.ink)
						ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
							ui.Text(c, desktop.Name).FontSize(m.Font(17)).FontWeight(500).SingleLine().Ellipsis("…")
							status := tr("Saved")
							if current {
								status = tr("Current desktop · ") + textutil.FirstNonEmpty(m.ConnectionLine(), tr("Connected"))
							}
							ui.Text(c, status).FontSize(m.Font(12)).TextColor(p.muted).MaxLines(2)
						})
						if current && m.Connected {
							ui.Box(c).Size(7, 7).Radius(4).Background(p.ok)
						}
					})
					if row.Clicked() {
						m.SwitchDesktop(desktop.ID)
					}
					more := m.homeAction(c, "more", tr("Desktop actions ")+desktop.Name, "dots")
					more.PopoverMenuButton(func(menu *ui.Menu) {
						if menu.Item(tr("Remove desktop")).Chosen() {
							m.RemoveDesktop(desktop.ID)
						}
					})
				})
			}
			if len(m.Desktops.Items) == 0 {
				ui.Text(c, tr("No saved desktops yet")).FontSize(m.Font(16)).TextColor(p.muted).Margin(12, 8)
			}
			if m.pill(c, "add-desktop", tr("Add desktop"), true).FillWidth().Clicked() {
				m.addDesktop()
			}
		})
	})
}

func (m *App) addDesktopView(c *ui.Context) {
	ui.Row(c).FillWidth().MinHeight(64).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		m.navigationBack(c, "/desktop/add")
		ui.Text(c, tr("Add desktop")).Grow(1).TextAlign(ui.Center).FontSize(m.Font(16)).FontWeight(600)
		ui.Box(c).Size(44, 44)
	})
	m.pairView(c)
}
