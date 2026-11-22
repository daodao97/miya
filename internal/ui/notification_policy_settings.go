package desktopui

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/notification"
	"miya/internal/widgets"
)

// notificationPolicyUI is the phone notification rules group in settings.
// view is nil until the background service answered; without one the group
// is not shown. start and end are the quiet hours being typed.
type notificationPolicyUI struct {
	view       *notification.PolicyView
	start, end string
	busy       bool
	err        string
	loadedAt   time.Time
}

func (app *App) notificationPolicyCall(op string, r notification.PolicyRequest) {
	p := &app.NotifPolicy
	if p.busy {
		return
	}
	p.busy = true
	c := app.C
	finish := func(raw json.RawMessage, err error) {
		p.busy, p.loadedAt = false, time.Now()
		if err != nil {
			p.err = err.Error()
			return
		}
		var v notification.PolicyView
		if json.Unmarshal(raw, &v) != nil {
			return
		}
		// A refresh keeps times being typed; a change shows what was saved.
		if p.view == nil || op != "notificationPolicy" || p.start == p.view.Quiet.Start && p.end == p.view.Quiet.End {
			p.start, p.end = v.Quiet.Start, v.Quiet.End
		}
		p.view, p.err = &v, ""
	}
	// Only the background service keeps phone notification rules.
	remote := c.State().Remote
	if remote == nil {
		p.busy = false
		p.loadedAt = time.Now()
		return
	}
	go func() {
		var out json.RawMessage
		err := remote.Call(op, r, &out)
		c.Dispatch(func() { finish(out, err) })
	}()
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

func (app *App) notificationPolicySettings(c *ui.Context) {
	p := &app.NotifPolicy
	if !p.busy && time.Since(p.loadedAt) > 10*time.Second {
		app.notificationPolicyCall("notificationPolicy", notification.PolicyRequest{})
	}
	if p.view == nil {
		return
	}
	v := *p.view
	setQuiet := func(enabled bool) {
		q := notification.QuietHours{Enabled: enabled, Start: p.start, End: p.end}
		app.notificationPolicyCall("notificationQuietHours", notification.PolicyRequest{Quiet: &q})
	}
	rows := []func(){func() {
		widgets.SettingRow(c, tr("Quiet hours"), tr("Phone notifications arrive as a badge only, without sound or banner."), func() {
			on := v.Quiet.Enabled
			ui.Switch(c.Key("quiet"), &on).Label(tr("Quiet hours"))
			if on != v.Quiet.Enabled {
				setQuiet(on)
			}
		})
	}}
	if v.Quiet.Enabled {
		rows = append(rows, func() {
			ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.TextInput(c.Key("quiet-start"), &p.start).Label(tr("Quiet hours start")).Placeholder("22:00").Width(90).FontSize(12)
				ui.TextInput(c.Key("quiet-end"), &p.end).Label(tr("Quiet hours end")).Placeholder("08:00").Width(90).FontSize(12)
				if (p.start != v.Quiet.Start || p.end != v.Quiet.End) && ui.Button(c.Key("quiet-save"), tr("Save")).Disabled(p.busy).Clicked() {
					setQuiet(true)
				}
			})
		})
	}
	for _, r := range v.Rules {
		rows = append(rows, func() {
			widgets.SettingRow(c, notificationRuleLabel(r), fmt.Sprintf(tr("Arrives as a badge only since %s, after you cleared it twice without opening it."), r.LearnedAt.Local().Format("01-02 15:04")), func() {
				if widgets.SmallButton(c.Key("forget-"+r.Category), tr("Alert again"), p.busy).Clicked() {
					app.notificationPolicyCall("notificationRuleForget", notification.PolicyRequest{Forget: r.Category})
				}
			})
		})
	}
	footnote := tr("Approvals, reviews, failures and results that need you always alert. Clear a kind of notice twice without opening it, and it arrives as a badge only; open its conversation twice, and it alerts again.")
	if p.err != "" {
		footnote += "\n" + tr(p.err)
	}
	widgets.SettingsGroup(c, tr("Phone notification rules"), footnote, rows...)
}
