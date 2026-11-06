package mobile

import (
	"fmt"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"miya/internal/prefs"
	"miya/internal/textutil"
	"miya/internal/widgets"
)

func (m *App) settingsView(c *ui.Context) {
	p := m.Pal
	ui.Row(c).FillWidth().MinHeight(64).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		m.navigationBack(c, "/settings")
		ui.Text(c, tr("Settings")).Grow(1).TextAlign(ui.Center).FontSize(m.Font(16)).FontWeight(600)
		ui.Box(c.Key("balance")).Size(44, 44)
	})
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(24, 16).Gap(12).Children(func() {
			m.languageSettings(c.Key("language"))
			ui.Text(c, tr("Desktop connections")).FontSize(m.Font(13)).TextColor(p.muted).Margin(0, 12)
			ui.Text(c, textutil.FirstNonEmpty(m.Host, tr("Desktop"))).FontSize(m.Font(17)).FontWeight(500).Margin(0, 12)
			if status := m.ConnectionLine(); status != "" {
				ui.Text(c, status).FontSize(m.Font(13)).TextColor(p.muted).Margin(0, 12)
			}
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				if m.pill(c, "reconnect", tr("Reconnect"), true).Grow(1).Clicked() {
					m.Connect(m.Link)
				}
				if m.pill(c, "manage-desktops", tr("Manage desktops"), false).Grow(1).Clicked() {
					m.openDesktopManager()
				}
			})
			if m.pill(c, "add-desktop", tr("Add desktop"), false).FillWidth().Clicked() {
				m.addDesktop()
			}
			ui.Text(c, tr("Conversations")).FontSize(m.Font(13)).TextColor(p.muted).Margin(0, 12)
			if m.pill(c, "message-options", tr("Model and permissions"), false).FillWidth().MinHeight(48).Clicked() {
				m.openOptions(m.composerAgent())
			}
			ui.Text(c, tr("Notifications")).FontSize(m.Font(13)).TextColor(p.muted).Margin(0, 12)
			ui.Row(c).FillWidth().Padding(18, 16).Gap(16).AlignItems(ui.Center).Background(p.surface).Radius(18).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
					ui.Text(c, tr("Mobile notifications")).FontSize(m.Font(16)).FontWeight(500)
					ui.Text(c, tr("Get alerts when tasks finish or need confirmation")).FontSize(m.Font(12)).TextColor(p.muted)
				})
				enabled := !m.PushDisabled
				if m.PhoneSwitch(c.Key("push-toggle"), &enabled, tr("Enable mobile notifications")).Changed() {
					m.setPushEnabled(enabled)
				}
			})
			ui.Text(c, tr("Also allow Miya notifications in Settings.")).FontSize(m.Font(12)).TextColor(p.muted).Margin(0, 12)
			ui.Row(c).FillWidth().Padding(14, 12).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf(tr("%d unread conversations"), len(m.State.Notifications.Unread))).Grow(1).FontSize(m.Font(14))
				if m.pill(c, "read-all", tr("Mark all as read"), false).Disabled(len(m.State.Notifications.Unread) == 0 || !m.Connected).Clicked() {
					m.markAllRead()
				}
			})
			if !m.PushDisabled && m.pushError != "" {
				ui.Text(c, m.pushError).FontSize(m.Font(13)).TextColor(widgets.Danger).Margin(0, 12)
			}
		})
	})
}

// Language names remain readable at accessibility sizes without sharing the
// desktop's compact segmented control.
func (m *App) languageSettings(c *ui.Context) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, tr("Language")).FontSize(m.Font(13)).TextColor(m.Pal.muted).PaddingX(12)
		selected := prefs.NormalizeInterfaceLanguage(prefs.Load("interfaceLanguage"))
		ui.Column(c).FillWidth().Padding(6).Radius(16).Background(m.Pal.surface).Role(ui.RoleRadioGroup).Label(tr("Interface language")).Children(func() {
			for _, choice := range []struct{ tag, label string }{{"", tr("Follow system")}, {"zh-Hans", i18n.Autonym("zh-Hans")}, {"en", i18n.Autonym("en")}} {
				row := m.phoneButton(c.Key(choice.tag)).FillWidth().MinWidth(0).Padding(12).Gap(12).
					Role(ui.RoleRadio).Checked(selected == choice.tag).Label(choice.label)
				row.Children(func() {
					ui.Text(c, choice.label).FontSize(m.Font(16)).Grow(1).MinWidth(0)
					if selected == choice.tag {
						widgets.Icon(c, "check", 18, m.Pal.ink)
					}
				})
				if row.Clicked() {
					widgets.SetInterfaceLanguage(choice.tag)
				}
			}
		})
		ui.Text(c, tr("Changes apply immediately on this device.")).FontSize(m.Font(12)).TextColor(m.Pal.muted).PaddingX(12)
	})
}

func (m *App) setPushEnabled(enabled bool) {
	m.PushDisabled = !enabled
	m.updateBadge()
	value := "off"
	if enabled {
		value = "on"
	}
	prefs.Save("phone-notifications", value)
	m.syncPush()
	if enabled {
		m.requestPush()
	}
}
