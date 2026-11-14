package mobile

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"miya/internal/prefs"
	"miya/internal/textutil"
	"miya/internal/widgets"
)

func mobileSettingsPage(path string) bool {
	return path == "/settings" || strings.HasPrefix(path, "/settings/") || path == "/options" || strings.HasPrefix(path, "/options/")
}

func (m *App) settingsBackground() ui.Color {
	if m.Dark {
		return m.Pal.Bg
	}
	return m.Pal.surface
}

func (m *App) settingsSurface() ui.Color {
	if m.Dark {
		return m.Pal.surface
	}
	return m.Pal.Bg
}

func (m *App) settingsHeader(c *ui.Context, path, title string) {
	ui.Row(c).FillWidth().MinHeight(56).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		m.navigationBack(c, path)
		ui.Text(c, title).Grow(1).MinWidth(0).Wrap().TextAlign(ui.Center).FontSize(min(m.Font(17), 24)).FontWeight(600)
		ui.Box(c.Key("balance")).Size(44, 44)
	})
}

func (m *App) settingsBody(c *ui.Context, body func()) {
	ui.Scroll(c.Key("settings-scroll")).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(16, 16, 28, 16).Gap(24).Children(body)
	})
}

func (m *App) settingsGroup(c *ui.Context, title, footer string, body func()) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(m.Font(13)).TextColor(m.Pal.muted).PaddingX(16).Wrap()
		}
		ui.Column(c).FillWidth().Background(m.settingsSurface()).Radius(12).Clip().Children(body)
		if footer != "" {
			ui.Text(c, footer).FontSize(m.Font(13)).TextColor(m.Pal.muted).LineHeight(1.3).PaddingX(16).Wrap()
		}
	})
}

func (m *App) settingsSeparator(c *ui.Context) {
	ui.Box(c).FillWidth().Height(1).MarginX(16).Background(m.Pal.line)
}

func (m *App) settingsButton(c *ui.Context, label string) ui.Element {
	row := m.phoneButton(c).FillWidth().MinWidth(0).MinHeight(52).Padding(12, 16).Radius(0).Label(label)
	if row.Pressed() && !row.IsDisabled() {
		row.Background(m.Pal.line).Opacity(1)
	}
	return row
}

// At accessibility sizes the value moves below its label. The whole row
// remains a single navigation target, regardless of the number of lines.
func (m *App) settingsNavigation(c *ui.Context, label, value string) ui.Element {
	return m.settingsButton(c, label).Gap(12).Children(func() {
		content := ui.Row(c).Grow(1).MinWidth(0).Gap(12).AlignItems(ui.Center)
		large := m.textScale > 1.3
		if large {
			content.Column().Gap(4).AlignItems(ui.Start)
		}
		content.Children(func() {
			text := ui.Text(c, label).FontSize(m.Font(17)).MinWidth(0).Wrap()
			if !large {
				text.Grow(1)
			}
			if value != "" {
				valueText := ui.Text(c, value).FontSize(m.Font(16)).TextColor(m.Pal.muted).MinWidth(0).Wrap()
				if !large {
					valueText.MaxWidth(c.Root().Bounds().W * 0.4).TextAlign(ui.End)
				}
			}
		})
		widgets.Icon(c, "chevron-right", 14, m.Pal.muted)
	})
}

func (m *App) settingsAction(c *ui.Context, label string) ui.Element {
	return m.settingsButton(c, label).Children(func() {
		ui.Text(c, label).FontSize(m.Font(17)).TextColor(m.Pal.link).MinWidth(0).Wrap()
	})
}

func (m *App) settingsView(c *ui.Context) {
	m.settingsHeader(c, "/settings", tr("Settings"))
	m.settingsBody(c, func() {
		m.settingsGroup(c.Key("general"), tr("General"), "", func() {
			if m.settingsNavigation(c.Key("language"), tr("Language"), m.interfaceLanguageName()).Clicked() {
				m.Router().Push("/settings/language")
			}
			m.settingsSeparator(c)
			if m.settingsNavigation(c.Key("voice"), tr("Voice input"), tr("WeChat")).Clicked() {
				m.Router().Push("/settings/voice")
			}
		})
		m.settingsGroup(c.Key("desktops"), tr("Desktop connections"), m.ConnectionLine(), func() {
			if m.settingsNavigation(c.Key("manage-desktops"), tr("Manage desktops"), textutil.FirstNonEmpty(m.DesktopName(), tr("Desktop"))).Clicked() {
				m.openDesktopManager()
			}
			m.settingsSeparator(c)
			if m.settingsAction(c.Key("reconnect"), tr("Reconnect")).Clicked() {
				m.Connect(m.Link)
			}
			m.settingsSeparator(c)
			if m.settingsAction(c.Key("add-desktop"), tr("Add desktop")).Clicked() {
				m.addDesktop()
			}
		})
		m.settingsGroup(c.Key("conversations"), tr("Conversations"), "", func() {
			if m.settingsNavigation(c.Key("message-options"), tr("Model and permissions"), "").Clicked() {
				m.openOptions(m.composerAgent())
			}
		})
		if m.State.AutomationWebhook {
			m.loadPhoneWebhook()
			m.settingsGroup(c.Key("integrations"), tr("Integrations"), "", func() {
				if m.settingsNavigation(c.Key("webhook"), tr("Miya webhook"), webhookSettingValue(m.Autos.Webhook)).Clicked() {
					m.Router().Push("/settings/webhook")
				}
			})
		}
		footer := tr("Get alerts when tasks finish or need confirmation") + "\n" + tr("Also allow Miya notifications in Settings.")
		m.settingsGroup(c.Key("notifications"), tr("Notifications"), footer, func() {
			ui.Row(c).FillWidth().MinHeight(52).Padding(4, 16).Gap(16).AlignItems(ui.Center).Children(func() {
				ui.Text(c, tr("Mobile notifications")).FontSize(m.Font(17)).Grow(1).MinWidth(0).Wrap()
				enabled := !m.PushDisabled
				if m.PhoneSwitch(c.Key("push-toggle"), &enabled, tr("Enable mobile notifications")).Changed() {
					m.setPushEnabled(enabled)
				}
			})
			m.settingsSeparator(c)
			row := m.settingsButton(c.Key("read-all"), tr("Mark all as read")).Disabled(len(m.State.Notifications.Unread) == 0 || !m.Connected)
			row.Children(func() {
				ui.Text(c, tr("Mark all as read")).FontSize(m.Font(17)).TextColor(m.Pal.link).Grow(1).MinWidth(0).Wrap()
				ui.Text(c, fmt.Sprintf("%d", len(m.State.Notifications.Unread))).Label(fmt.Sprintf(tr("%d unread conversations"), len(m.State.Notifications.Unread))).FontSize(m.Font(16)).TextColor(m.Pal.muted).Shrink(0)
			})
			if row.Clicked() {
				m.markAllRead()
			}
		})
		if !m.PushDisabled && m.pushError != "" {
			ui.Text(c, m.pushError).FontSize(m.Font(13)).TextColor(widgets.Danger).PaddingX(16).Wrap()
		}
	})
}

func (m *App) interfaceLanguageName() string {
	if selected := prefs.NormalizeInterfaceLanguage(prefs.Load("interfaceLanguage")); selected != "" {
		return i18n.Autonym(selected)
	}
	return tr("Follow system")
}

func (m *App) languageSettingsPage(c *ui.Context) {
	m.settingsHeader(c, "/settings/language", tr("Language"))
	m.settingsBody(c, func() { m.languageSettings(c.Key("language")) })
}

func (m *App) voiceSettingsPage(c *ui.Context) {
	m.settingsHeader(c, "/settings/voice", tr("Voice input"))
	m.settingsBody(c, func() { m.voiceSettings(c.Key("voice")) })
}

func (m *App) languageSettings(c *ui.Context) {
	selected := prefs.NormalizeInterfaceLanguage(prefs.Load("interfaceLanguage"))
	m.settingsGroup(c, "", tr("Changes apply immediately on this device."), func() {
		ui.Column(c).FillWidth().Role(ui.RoleRadioGroup).Label(tr("Interface language")).Children(func() {
			for index, choice := range []struct{ tag, label string }{{"", tr("Follow system")}, {"zh-Hans", i18n.Autonym("zh-Hans")}, {"en", i18n.Autonym("en")}} {
				if index > 0 {
					m.settingsSeparator(c)
				}
				row := m.settingsButton(c.Key(choice.tag), choice.label).Gap(12).Role(ui.RoleRadio).Checked(selected == choice.tag)
				row.Children(func() {
					ui.Text(c, choice.label).FontSize(m.Font(17)).Grow(1).MinWidth(0).Wrap()
					if selected == choice.tag {
						widgets.Icon(c, "check", 18, m.Pal.link)
					}
				})
				if row.Clicked() {
					widgets.SetInterfaceLanguage(choice.tag)
				}
			}
		})
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
