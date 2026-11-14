package widgets

import (
	"github.com/egoist/mygo/ui"
	"miya/internal/automation"
	"miya/internal/textutil"
)

// WebhookInbox shows the application-level address even when there are no
// tasks. Individual task pages only describe their message subscriptions.
func WebhookInbox(c *ui.Context, v automation.WebhookView, toggle func(bool)) {
	ui.Column(c).FillWidth().Padding(16).Gap(10).Border(1, Line).Radius(12).Children(func() {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("Miya webhook")).Grow(1).FontSize(14).FontWeight(600)
			if toggle != nil {
				enabled := v.Enabled
				if ui.Switch(c.Key("enabled"), &enabled).Label(tr("Enable webhook")).Changed() {
					toggle(enabled)
				}
			}
		})
		ui.Text(c, tr("Messages are received by Miya and routed to matching automations.")).FontSize(12).TextColor(Muted)
		ui.Text(c, v.Status()).FontSize(12).TextColor(Muted)
		if v.URL != "" {
			ui.Text(c, v.URL).FontSize(12).Font(MonoFont).Selectable()
			if SmallButton(c.Key("copy"), tr("Copy webhook URL"), false).AlignSelf(ui.Start).Clicked() {
				c.WriteClipboard(v.URL)
			}
			ui.Text(c, tr("POST text or JSON to this URL while Miya is online.")).FontSize(12).TextColor(Muted)
		}
		if len(v.Messages) > 0 {
			ui.Text(c, tr("Recent messages")).FontSize(12).FontWeight(600).Margin(6, 0, 0, 0)
			for i := len(v.Messages) - 1; i >= max(0, len(v.Messages)-5); i-- {
				message := v.Messages[i]
				ui.Column(c.Key(message.ID)).FillWidth().Gap(4).Padding(8, 0).Children(func() {
					ui.Text(c, automation.FormatTime(message.At)).FontSize(11).TextColor(Muted)
					ui.Text(c, textutil.ClipRunes(message.Data, 200)).FontSize(12).MaxLines(3).Selectable()
					ui.Text(c, automation.WebhookDeliveryText(message)).FontSize(12).TextColor(Muted)
				})
			}
		}
	})
}
