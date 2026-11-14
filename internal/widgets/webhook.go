package widgets

import (
	"github.com/egoist/mygo/ui"
	"miya/internal/automation"
	"miya/internal/textutil"
)

// WebhookTone sorts a delivery by how it went, for its status dot.
type WebhookTone int

const (
	// WebhookUnrouted reached Miya but no automation took it.
	WebhookUnrouted WebhookTone = iota
	// WebhookDelivered woke, queued or was gathered by an automation.
	WebhookDelivered
	// WebhookProblem failed to route or to start.
	WebhookProblem
)

func WebhookMessageTone(m automation.WebhookMessage) WebhookTone {
	if len(m.Routes) == 0 {
		if m.Detail != "" && m.Detail != "No matching automation" {
			return WebhookProblem
		}
		return WebhookUnrouted
	}
	tone := WebhookUnrouted
	for _, r := range m.Routes {
		switch r.Outcome {
		case automation.WakeFailed, automation.WakeDropped:
			return WebhookProblem
		case automation.WakeStarted, automation.WakeQueued, automation.WakeGathered:
			tone = WebhookDelivered
		}
	}
	return tone
}

// RecentWebhookMessages returns up to n messages, newest first.
func RecentWebhookMessages(v automation.WebhookView, n int) []automation.WebhookMessage {
	out := make([]automation.WebhookMessage, 0, min(n, len(v.Messages)))
	for i := len(v.Messages) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, v.Messages[i])
	}
	return out
}

// WebhookToneColor is the dot color of a delivery, given the page's ok
// and muted colors.
func WebhookToneColor(t WebhookTone, ok, muted ui.Color) ui.Color {
	switch t {
	case WebhookDelivered:
		return ok
	case WebhookProblem:
		return webhookBad
	}
	return muted
}

// WebhookStatusColor colors the endpoint's state: ready, broken, or off
// and still registering.
func WebhookStatusColor(v automation.WebhookView, ok, muted ui.Color) ui.Color {
	switch {
	case !v.Enabled:
		return muted
	case v.Error != "":
		return webhookBad
	case v.URL != "":
		return ok
	}
	return webhookWait
}

// WebhookHelp explains the shared address wherever it is shown.
func WebhookHelp() string {
	return tr("Webhook address for scripts, CI and other services. Matching automations wake on the messages it receives.")
}

var webhookOK = ui.Hex("#15803d")

var webhookBad = ui.Hex("#dc2626")

var webhookWait = ui.Hex("#b45309")

// Dot is a small filled status circle.
func Dot(c *ui.Context, color ui.Color, size float32) ui.Element {
	return ui.Box(c).Size(size, size).Shrink(0).Radius(size / 2).Background(color)
}

// WebhookSettings is the desktop Settings pane for Miya's shared inbound
// address. Automations are one consumer of it; the phone and the desktop
// both show what arrived.
func WebhookSettings(c *ui.Context, v *automation.WebhookView, toggle func(bool)) {
	if v == nil {
		ui.Row(c).Gap(8).AlignItems(ui.Center).PaddingX(2).Children(func() {
			ui.Spinner(c).FontSize(12)
			ui.Text(c, tr("Loading…")).FontSize(12).TextColor(Muted)
		})
		return
	}
	SettingsHeader(c, tr("Miya webhook"), WebhookHelp(), nil)
	rows := []func(){func() {
		ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(16).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, tr("Enable webhook")).FontSize(13).TextColor(Ink)
				ui.Row(c).MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
					Dot(c, WebhookStatusColor(*v, webhookOK, Muted), 7)
					ui.Text(c, v.Status()).FontSize(11).TextColor(Muted).MinWidth(0)
				})
			})
			if toggle != nil {
				enabled := v.Enabled
				if ui.Switch(c.Key("enabled"), &enabled).Shrink(0).Label(tr("Enable webhook")).Changed() {
					toggle(enabled)
				}
			}
		})
	}}
	if v.URL != "" {
		rows = append(rows, func() {
			ui.Text(c, tr("Address")).FontSize(11).TextColor(Muted)
			ui.Row(c).FillWidth().MinWidth(0).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Grow(1).MinWidth(0).Padding(9, 11).Radius(8).Background(Bubble).Children(func() {
					ui.Text(c, v.URL).FontSize(12).Font(MonoFont).LineHeight(1.5).Selectable()
				})
				if SmallButton(c.Key("copy"), tr("Copy webhook URL"), false).Shrink(0).Clicked() {
					c.WriteClipboard(v.URL)
				}
			})
		})
	}
	SettingsGroup(c.Key("endpoint"), "", tr("POST text or JSON to this URL while Miya is online."), rows...)

	messages := RecentWebhookMessages(*v, 8)
	if len(messages) == 0 {
		SettingsGroup(c.Key("messages"), tr("Recent messages"), "", func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				Icon(c, "message-circle", 15, Muted)
				ui.Text(c, tr("No messages yet")).FontSize(12).TextColor(Muted)
			})
		})
		return
	}
	rows = make([]func(), 0, len(messages))
	for _, message := range messages {
		message := message
		rows = append(rows, func() {
			ui.Row(c.Key(message.ID)).FillWidth().MinWidth(0).Gap(10).AlignItems(ui.Start).Children(func() {
				Dot(c, WebhookToneColor(WebhookMessageTone(message), webhookOK, Muted), 7).Margin(5, 0, 0, 0)
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					ui.Row(c).FillWidth().MinWidth(0).Gap(12).AlignItems(ui.Start).Children(func() {
						ui.Text(c, automation.WebhookDeliveryText(message)).Grow(1).MinWidth(0).FontSize(13).TextColor(Ink).MaxLines(3)
						ui.Text(c, automation.FormatTime(message.At)).Shrink(0).FontSize(11).TextColor(Muted).SingleLine()
					})
					ui.Text(c, textutil.ClipRunes(message.Data, 240)).FontSize(11).Font(MonoFont).TextColor(Muted).LineHeight(1.5).MaxLines(2).Selectable()
				})
			})
		})
	}
	SettingsGroup(c.Key("messages"), tr("Recent messages"), "", rows...)
}
