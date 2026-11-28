package nsui

import (
	"fmt"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/notification"
)

func notificationPolicySettings(cx *core.Scope, c Host) core.Node {
	view := core.NewSignal[*notification.PolicyView](cx, nil)
	start := core.NewSignal(cx, "")
	end := core.NewSignal(cx, "")
	pending := core.NewSignal(cx, false)
	message := core.NewSignal(cx, "")
	call := func(op string, req notification.PolicyRequest) {
		cl := c.State().Remote
		if pending.Get() || cl == nil || !cl.Connected() {
			return
		}
		pending.Set(true)
		go func() {
			var next notification.PolicyView
			err := cl.Call(op, req, &next)
			cx.Post(func() {
				pending.Set(false)
				if c.State().Remote != cl {
					return
				}
				if err != nil {
					if op != "notificationPolicy" {
						message.Set(err.Error())
					}
					return
				}
				view.Set(&next)
				start.Set(next.Quiet.Start)
				end.Set(next.Quiet.End)
				message.Set("")
			})
		}()
	}
	cx.Post(func() { call("notificationPolicy", notification.PolicyRequest{}) })
	save := func(on bool) {
		q := notification.QuietHours{Enabled: on, Start: start.Get(), End: end.Get()}
		call("notificationQuietHours", notification.PolicyRequest{Quiet: &q})
	}
	return core.Dynamic(func(cx *core.Scope) core.Node {
		v := view.Get()
		if v == nil {
			return core.Group(nil)
		}
		nodes := []core.Node{
			ui.FormSection{Children: []core.Node{ui.FormRow{Title: tr("Quiet hours"),
				Detail:  tr("Phone notifications arrive as a badge only, without sound or banner."),
				Control: settingsToggle("quiet-hours", tr("Quiet hours"), func() bool { return v.Quiet.Enabled }, save, pending.Get),
			}}},
		}
		if v.Quiet.Enabled {
			nodes = append(nodes, settingsActions(
				settingsField(tr("Quiet hours start"), ui.Input{TestID: "quiet-hours-start", Label: tr("Quiet hours start"), Placeholder: "22:00", Value: start.Get, OnChange: start.Set, Style: core.Style{Width: core.Pt(105)}}),
				settingsField(tr("Quiet hours end"), ui.Input{TestID: "quiet-hours-end", Label: tr("Quiet hours end"), Placeholder: "08:00", Value: end.Get, OnChange: end.Set, Style: core.Style{Width: core.Pt(105)}}),
				settingsButton("quiet-hours-save", tr("Save"), func() { save(true) }, pending.Get),
			))
		}
		if len(v.Rules) > 0 {
			var rows []core.Node
			for _, r := range v.Rules {
				rows = append(rows, ui.FormRow{Title: notificationRuleLabel(r),
					Control: settingsButton("notification-rule-forget-"+r.Category, tr("Alert again"), func() {
						call("notificationRuleForget", notification.PolicyRequest{Forget: r.Category})
					}, pending.Get),
				})
			}
			nodes = append(nodes, ui.FormSection{Children: rows})
		}
		return settingsColumn(append(nodes, settingsText(cx, message.Get))...)
	})
}

func notificationRuleLabel(r notification.Rule) string {
	switch r.Kind {
	case "completion":
		if r.Name == "" {
			return tr("Finished conversations")
		}
		return fmt.Sprintf(tr("Finished conversations in %s"), r.Name)
	case "automation":
		return fmt.Sprintf(tr("Results of automation %s"), r.Name)
	case "probe":
		return fmt.Sprintf(tr("Failing condition checks of %s"), r.Name)
	case "webhook":
		return tr("Webhooks no automation took")
	case "briefing":
		return tr("Daily briefing")
	}
	return r.Category
}

func phoneNotificationGroup(cx *core.Scope, c Host) ui.SettingsGroup {
	return ui.SettingsGroup{ID: "phone-notification-rules", Title: tr("Phone notification rules"),
		Items: []ui.SettingsItem{{ID: "phone-notification-policy", Content: core.Func(func(cx *core.Scope) core.Node {
			return notificationPolicySettings(cx, c)
		})}}}
}
