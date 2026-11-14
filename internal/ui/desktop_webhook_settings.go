package desktopui

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/automation"
	"miya/internal/widgets"
)

// webhookUI is the Webhook settings pane's copy of Miya's shared inbound
// endpoint; the service owns it.
type webhookUI struct {
	View     *automation.WebhookView
	loading  bool
	loadedAt time.Time
	err      string
}

// ShowWebhookSettings opens Settings on the Webhook pane.
func (app *App) ShowWebhookSettings() {
	app.Section, app.SettingsTab = "Settings", webhookSettingsTab
	app.Webhook.loadedAt = time.Time{}
}

func (app *App) loadWebhook(action string) {
	w := &app.Webhook
	if w.loading && action == "" {
		return
	}
	w.loading = true
	app.autoCall("automationWebhook", automation.Request{Action: action}, func(raw json.RawMessage, err error) {
		w.loading, w.loadedAt = false, time.Now()
		if err != nil {
			message := err.Error()
			if strings.Contains(strings.ToLower(message), "unknown operation") || strings.Contains(message, "Unknown automation operation") {
				message = tr("Waiting for running conversations to finish before the webhook update takes effect.")
			}
			if w.View == nil {
				w.View = &automation.WebhookView{Enabled: true, Error: message}
			} else {
				w.err = message
			}
			return
		}
		var v automation.WebhookView
		if json.Unmarshal(raw, &v) == nil {
			w.View, w.err = &v, ""
		}
	})
}

func (app *App) webhookSettings(c *ui.Context) {
	w := &app.Webhook
	if !w.loading && time.Since(w.loadedAt) > 5*time.Second {
		app.loadWebhook("")
	}
	if w.View != nil || w.loading {
		c.After(5 * time.Second)
	}
	widgets.WebhookSettings(c.Key("webhook"), w.View, func(enabled bool) {
		action := "disable"
		if enabled {
			action = "enable"
		}
		app.loadWebhook(action)
	})
	if w.err != "" {
		ui.Text(c, w.err).FontSize(12).TextColor(widgets.Danger).PaddingX(2)
	}
}
