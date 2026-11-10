package desktopui

import (
	"encoding/json"
	"fmt"

	"github.com/egoist/mygo/ui"

	"miya/internal/automation"
	"miya/internal/widgets"
)

// wakeLogShown is how many of an automation's wakes the detail page lists.
const wakeLogShown = 10

// nextRunText says when an automation next runs: its next scheduled time,
// or that its triggers wake it.
func nextRunText(v automation.View) string {
	switch {
	case !v.Enabled:
		return tr("Paused")
	case v.Schedule == "":
		return tr("Waiting for a trigger")
	}
	return automation.FormatTime(v.Next)
}

func triggerIcon(t automation.Trigger) string {
	switch t.Kind {
	case automation.TriggerCondition:
		if t.Condition != nil && t.Condition.Probe == automation.ProbeCommand {
			return "terminal-2"
		}
		return "device-floppy"
	case automation.TriggerIngress:
		return "globe"
	case automation.TriggerFile:
		return "file"
	case automation.TriggerAfter:
		return "arrow-right"
	case automation.TriggerSystem:
		return "device-desktop"
	}
	return "bulb"
}

// triggerState says what a trigger last saw.
func triggerState(t automation.Trigger, st automation.TriggerState, ok bool) string {
	switch {
	case t.Disabled:
		return tr("Off")
	case !ok:
		return tr("Not checked yet")
	case st.Error != "":
		return tr("Check failed: ") + st.Error
	case t.Kind != automation.TriggerCondition:
		return tr("Watching")
	}
	text := st.Value
	if !st.LastProbe.IsZero() {
		text += " · " + automation.FormatTime(st.LastProbe)
	}
	if st.Fired {
		text += " · " + tr("Woke it; waits to recover")
	}
	return text
}

// triggersView lists how an automation wakes besides its schedule, with
// what each trigger saw, a switch for each, and the latest wakes.
func (app *App) triggersView(c *ui.Context, v automation.View) {
	if len(v.Triggers) > 0 || v.Policy.Gate != nil {
		ui.Column(c.Key("triggers")).FillWidth().Gap(10).Padding(16).Border(1, widgets.Line).Radius(12).Children(func() {
			ui.Text(c, tr("Triggers")).FontSize(12).TextColor(widgets.Muted)
			for i, t := range v.Triggers {
				app.triggerRow(c.Key("trigger:"+t.ID), v, i)
			}
			if g := v.Policy.Gate; g != nil {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					widgets.Icon(c, "shield-check", 14, widgets.Muted)
					ui.Text(c, tr("Runs only ")+automation.DescribeTrigger(automation.Trigger{Kind: automation.TriggerCondition, Condition: g}, nil)).FontSize(13)
				})
			}
			ui.Text(c, automation.PolicyText(v.Policy)).FontSize(12).TextColor(widgets.Muted)
		})
	}
	if len(v.Wakes) == 0 {
		return
	}
	ui.Text(c.Key("wakes-title"), tr("Recent wakes")).FontWeight(600).Margin(8, 0, 0, 0)
	ui.Column(c.Key("wakes")).FillWidth().Gap(2).Children(func() {
		shown := 0
		for i := len(v.Wakes) - 1; i >= 0 && shown < wakeLogShown; i-- {
			w := v.Wakes[i]
			shown++
			ui.Row(c.Key(i)).FillWidth().Gap(10).Padding(6, 4, 6, 4).AlignItems(ui.Center).Children(func() {
				ui.Text(c, automation.FormatTime(w.At)).FontSize(12).TextColor(widgets.Muted).Width(120).Shrink(0).SingleLine()
				ui.Text(c, automation.SourceLabel(w.Source)).FontSize(12).Width(110).Shrink(0).SingleLine().Ellipsis("…")
				ui.Text(c, w.Reason).FontSize(12).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				outcome := automation.OutcomeLabel(w.Outcome)
				if w.Detail != "" {
					outcome += " · " + w.Detail
				}
				color := widgets.Muted
				if w.Outcome == automation.WakeStarted {
					color = widgets.Ink
				}
				ui.Text(c, outcome).FontSize(12).TextColor(color).Width(220).Shrink(0).SingleLine().Ellipsis("…")
			})
		}
	})
}

func (app *App) triggerRow(c *ui.Context, v automation.View, i int) {
	t := v.Triggers[i]
	st, ok := v.TriggerStates[t.ID]
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			widgets.Icon(c, triggerIcon(t), 14, widgets.Muted)
			text := v.TriggerTexts[t.ID]
			if text == "" {
				text = automation.DescribeTrigger(t, nil)
			}
			ui.Text(c, text).FontSize(13).FontWeight(500).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			on := !t.Disabled
			if ui.Switch(c.Key("on"), &on).Shrink(0).Changed() {
				next := v.Automation
				next.Triggers = append([]automation.Trigger(nil), v.Triggers...)
				next.Triggers[i].Disabled = !on
				app.autoCall("automationSave", automation.Request{Automation: &next}, func(_ json.RawMessage, err error) {
					if err != nil {
						app.Autos.err = err.Error()
					}
					app.loadAutomations()
				})
			}
		})
		ui.Text(c, triggerState(t, st, ok)).FontSize(12).TextColor(widgets.Muted).SingleLine().Ellipsis("…")
		if t.Kind != automation.TriggerIngress || t.Disabled {
			return
		}
		if v.WakeCommand != "" {
			ui.Text(c, v.WakeCommand+" [-data text|@file|-]").FontSize(12).Font(widgets.MonoFont).Selectable()
		}
		if url := v.WakeURLs[t.ID]; url != "" {
			ui.Text(c, fmt.Sprintf("curl -X POST -H 'Authorization: Bearer %s' --data @payload.json %s", t.Token, url)).FontSize(12).Font(widgets.MonoFont).Selectable()
		}
	})
}

// draftScheduleRow is a draft's schedule, with its next times.
func (app *App) draftScheduleRow(c *ui.Context, d *automation.View) {
	app.draftRow(c, "schedule", "clock", tr("Schedule"), d.Schedule, d.TimeMissing, func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if d.TimeMissing {
					ui.Text(c, tr("No time specified. Using ")+automation.DescribeCron(d.Schedule)).TextColor(autoWarn)
				} else {
					ui.Text(c, automation.DescribeCron(d.Schedule)).FontWeight(500)
				}
				codeChip(c, d.Schedule)
			})
			ui.Row(c).Gap(6).Children(func() {
				for i, t := range d.Upcoming {
					chip := ui.Text(c.Key(i), fmt.Sprintf(tr("%d/%d %s %s"), t.Month(), t.Day(), tr(automation.WeekdayNames[t.Weekday()]), t.Format("15:04"))).
						FontSize(12).Padding(1, 8, 1, 8).Radius(6).Background(widgets.Subtle).TextColor(widgets.Muted)
					if i == 0 {
						chip.TextColor(widgets.Ink)
					}
				}
			})
		})
	})
}

// draftTriggersRow shows what else wakes a drafted automation. Changing it
// means rewording the request and drafting again.
func draftTriggersRow(c *ui.Context, d *automation.View) {
	ui.Row(c).FillWidth().Gap(12).Padding(12, 16, 12, 16).AlignItems(ui.Start).Children(func() {
		widgets.Icon(c, "bulb", 14, widgets.Muted)
		ui.Text(c, tr("Wakes")).FontSize(13).TextColor(widgets.Muted).Width(110).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			for i, t := range d.Triggers {
				ui.Row(c.Key(i)).Gap(6).AlignItems(ui.Center).Children(func() {
					widgets.Icon(c, triggerIcon(t), 12, widgets.Muted)
					ui.Text(c, automation.DescribeTrigger(t, nil)).FontWeight(500)
				})
			}
			if g := d.Policy.Gate; g != nil {
				ui.Text(c, tr("Runs only ")+automation.DescribeTrigger(automation.Trigger{Kind: automation.TriggerCondition, Condition: g}, nil)).FontWeight(500)
			}
			if d.HasTrigger(automation.TriggerIngress) || d.HasTrigger(automation.TriggerFile) {
				ui.Text(c, tr("Can be woken from outside Miya; what comes with a wake reaches the agent as data. Consider running it in a separate worktree.")).FontSize(12).TextColor(autoWarn)
			}
		})
	})
}
