package mobile

import (
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/automation"
	"miya/internal/remote"
	"miya/internal/textutil"
	"miya/internal/widgets"
)

// loadPhoneWebhook asks the desktop for Miya's shared inbound endpoint, at
// most every five seconds. The reply lands in m.Autos.Webhook.
func (m *App) loadPhoneWebhook() {
	a := &m.Autos
	if !m.Connected || m.Conn == nil || !m.State.AutomationWebhook || time.Since(a.WebhookRequestedAt) < 5*time.Second {
		return
	}
	a.WebhookRequestedAt = time.Now()
	conn := m.Conn
	go func() { _ = conn.Send(remote.Request{Op: "automation-webhook"}) }()
}

// webhookSettingValue is the short state shown beside the settings row.
func webhookSettingValue(v *automation.WebhookView) string {
	switch {
	case v == nil:
		return ""
	case !v.Enabled:
		return tr("Off")
	}
	return tr("On")
}

func (m *App) webhookSettingsPage(c *ui.Context) {
	p, a := m.Pal, &m.Autos
	m.loadPhoneWebhook()
	if m.Connected {
		c.After(time.Second)
	}
	m.settingsHeader(c, "/settings/webhook", tr("Webhook"))
	m.settingsBody(c, func() {
		v := a.Webhook
		if v == nil {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).PaddingX(16).Children(func() {
				if m.Connected {
					ui.Spinner(c)
					ui.Text(c, tr("Loading…")).FontSize(m.Font(15)).TextColor(p.muted)
				} else {
					ui.Text(c, m.ConnectionLine()).FontSize(m.Font(15)).TextColor(p.muted).Wrap()
				}
			})
			return
		}
		footer := widgets.WebhookHelp()
		if v.URL != "" {
			footer += "\n" + tr("POST text or JSON to this URL while Miya is online.")
		}
		m.settingsGroup(c.Key("endpoint"), "", footer, func() {
			ui.Row(c).FillWidth().MinHeight(52).Padding(4, 16).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, tr("Miya webhook")).FontSize(m.Font(17)).Grow(1).MinWidth(0).Wrap()
				widgets.Dot(c, widgets.WebhookStatusColor(*v, p.ok, p.muted), 8)
				ui.Text(c, v.Status()).FontSize(m.Font(15)).TextColor(p.muted).MaxWidth(c.Root().Bounds().W * 0.45).Wrap().TextAlign(ui.End)
			})
			if v.URL == "" {
				return
			}
			m.settingsSeparator(c)
			ui.Column(c).FillWidth().Padding(12, 16).Gap(6).Children(func() {
				ui.Text(c, tr("Address")).FontSize(m.Font(13)).TextColor(p.muted)
				ui.Text(c, v.URL).FontSize(m.Font(14)).Font(widgets.MonoFont).LineHeight(1.45).Wrap().Selectable()
			})
			m.settingsSeparator(c)
			label := tr("Copy webhook URL")
			if time.Since(a.webhookCopiedAt) < 2*time.Second {
				label = tr("Copied")
			}
			if m.settingsAction(c.Key("copy"), label).Clicked() {
				c.WriteClipboard(v.URL)
				a.webhookCopiedAt = time.Now()
			}
		})
		messages := widgets.RecentWebhookMessages(*v, 8)
		m.settingsGroup(c.Key("messages"), tr("Recent messages"), "", func() {
			if len(messages) == 0 {
				ui.Row(c).FillWidth().MinHeight(52).Padding(4, 16).AlignItems(ui.Center).Children(func() {
					ui.Text(c, tr("No messages yet")).FontSize(m.Font(15)).TextColor(p.muted)
				})
				return
			}
			for i, message := range messages {
				if i > 0 {
					m.settingsSeparator(c.Key(i))
				}
				ui.Row(c.Key(message.ID)).FillWidth().Padding(12, 16).Gap(10).AlignItems(ui.Start).Children(func() {
					widgets.Dot(c, widgets.WebhookToneColor(widgets.WebhookMessageTone(message), p.ok, p.muted), 8).Margin(6, 0, 0, 0)
					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
						ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Start).Children(func() {
							ui.Text(c, automation.WebhookDeliveryText(message)).FontSize(m.Font(15)).Grow(1).MinWidth(0).MaxLines(3)
							ui.Text(c, automation.FormatTime(message.At)).FontSize(m.Font(12)).TextColor(p.muted).Shrink(0)
						})
						ui.Text(c, textutil.ClipRunes(message.Data, 240)).FontSize(m.Font(12)).Font(widgets.MonoFont).TextColor(p.muted).LineHeight(1.4).MaxLines(3).Selectable()
					})
				})
			}
		})
	})
}
