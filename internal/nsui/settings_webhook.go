package nsui

import (
	"encoding/json"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/automation"
	svcclient "miya/internal/service/client"
	"miya/internal/textutil"
)

func webhookSettings(c Host) ui.SettingsGroup {
	return ui.SettingsGroup{ID: "webhook", Items: []ui.SettingsItem{{
		ID: "webhook", Title: tr("Enable webhook"), Content: core.Func(func(cx *core.Scope) core.Node {
			state := core.NewSignal[*automation.WebhookView](cx, nil)
			busy := core.NewSignal(cx, false)
			message := core.NewSignal(cx, "")
			load := func(action string) {
				if busy.Get() {
					return
				}
				busy.Set(true)
				client := c.State().Remote
				callAutomationService(cx, c, "automationWebhook", automation.Request{Action: action}, func(raw json.RawMessage, err error) {
					busy.Set(false)
					if c.State().Remote != client {
						return
					}
					var view automation.WebhookView
					if err == nil {
						err = json.Unmarshal(raw, &view)
					}
					if err != nil {
						message.Set(err.Error())
						return
					}
					message.Set("")
					state.Set(&view)
				})
			}
			var refresh func()
			refresh = func() {
				load("")
				cx.AfterFunc(5*time.Second, refresh)
			}
			target := core.NewMemo(cx, Live(func() *svcclient.Client { return c.State().Remote }))
			cx.Effect(func() {
				target.Get()
				cx.Untrack(func() { state.Set(nil); message.Set(""); load("") })
			})
			cx.AfterFunc(5*time.Second, refresh)
			on := func() bool { return state.Get() != nil && state.Get().Enabled }
			return settingsColumn(
				ui.FormSection{Children: []core.Node{ui.FormRow{
					Title: tr("Enable webhook"), Detail: tr("Messages are received by Miya and routed to matching automations."),
					Control: settingsToggle("settings-webhook-enabled", tr("Enable webhook"), on, func(on bool) {
						action := "disable"
						if on {
							action = "enable"
						}
						load(action)
					}, func() bool { return busy.Get() || state.Get() == nil }),
				}}},
				settingsText(cx, message.Get),
				core.Dynamic(func(cx *core.Scope) core.Node {
					v := state.Get()
					if v == nil {
						return core.Group(nil)
					}
					nodes := []core.Node{settingsText(cx, v.Status)}
					if v.URL != "" {
						nodes = append(nodes, settingsText(cx, func() string { return v.URL }), settingsActions(
							settingsButton("settings-webhook-copy", tr("Copy webhook URL"), func() { cx.SetClipboard(v.URL) }, nil),
						), settingsText(cx, func() string { return tr("POST text or JSON to this URL while Miya is online.") }))
					}
					if len(v.Messages) != 0 {
						var messages []core.Node
						for i := len(v.Messages) - 1; i >= max(0, len(v.Messages)-5); i-- {
							m := v.Messages[i]
							messages = append(messages, ui.FormRow{Title: textutil.ClipRunes(m.Data, 200),
								Detail: automation.FormatTime(m.At) + " · " + automation.WebhookDeliveryText(m)})
						}
						nodes = append(nodes, ui.FormSection{Title: tr("Recent messages"), Children: messages})
					}
					return settingsColumn(nodes...)
				}),
			)
		}),
	}}}
}
