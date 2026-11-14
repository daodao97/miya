package nsui

import (
	"fmt"
	"math"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	agentmeta "miya/internal/agent/catalog"
	"miya/internal/agent/quota"
	"miya/internal/nswidgets"
	"miya/internal/prefs"
)

func usageClock(cx *core.Scope) func() time.Time {
	now := core.NewSignal(cx, cx.Now())
	var tick func()
	tick = func() { now.Set(cx.Now()); cx.AfterFunc(time.Minute, tick) }
	cx.AfterFunc(time.Minute, tick)
	return now.Get
}

func (d *desktopUsage) panelView(cx *core.Scope) core.Node {
	now := usageClock(cx)
	cx.Effect(func() {
		appearance := Live(func() string { return prefs.NormalizeAppearance(prefs.Load("appearance")) })()
		cx.Untrack(func() { setSettingsAppearance(cx, appearance) })
	})
	cx.OnKey("escape", func() { d.panel.Hide(); d.resetOpen = false; d.changed() })
	return core.Dynamic(func(cx *core.Scope) core.Node {
		d.revision.Get()
		Live(func() string { return prefs.Load("interfaceLanguage") })()
		var tabs []core.Node
		for _, agent := range usageAgents() {
			tabs = append(tabs, ui.Button{TestID: "usage-tab-" + agent, Text: quota.AgentName(agent), Variant: ui.ButtonGhost, OnPress: func() { d.panelAgent = agent; d.resetOpen = false; d.changed() }})
		}
		tabs = append(tabs, ui.IconButton{TestID: "usage-account", Icon: nswidgets.IconOf("settings"), Label: tr("Account"), OnPress: func() { d.showSettings("account") }})
		r := d.reading(d.panelAgent)
		var body core.Node
		if d.resetOpen && d.reset != nil && d.reset.Agent == r.Agent {
			body = d.resetConfirmation(cx)
		} else {
			body = d.readingView(cx, r, now())
		}
		return core.View{TestID: "usage-panel", Style: ui.FillRest.Merge(core.Style{Gap: 12, Padding: core.All(14)}), Children: []core.Node{
			settingsActions(tabs...),
			core.Scroll{Style: ui.FillRest, Children: []core.Node{body}},
			settingsActions(settingsText(cx, func() string { return r.Plan }), core.Text{TestID: "usage-status", Content: usagePanelStatus(r, d.busy, now()), Style: core.Style{FontSize: 11, FlexGrow: 1}}, settingsButton("usage-refresh", tr("Refresh usage"), d.refresh, func() bool { d.revision.Get(); return d.busy })),
		}}
	})
}
func (d *desktopUsage) subscriptionCard(cx *core.Scope, agent string) core.Node {
	now := usageClock(cx)
	return core.Dynamic(func(cx *core.Scope) core.Node {
		r := d.reading(agent)
		nodes := []core.Node{core.Text{Content: quota.AgentName(agent) + " · " + r.Plan, Style: core.Style{FontSize: 14, FontWeight: ui.Semibold}}}
		if d.resetOpen && d.reset != nil && d.reset.Agent == agent {
			nodes = append(nodes, d.resetConfirmation(cx))
		} else {
			nodes = append(nodes, d.readingView(cx, r, now()))
		}
		nodes = append(nodes, settingsText(cx, func() string { return usagePanelStatus(r, d.busy, now()) }))
		return core.View{TestID: "usage-card-" + agent, Style: core.Style{Gap: 14, Padding: core.All(14), BorderWidth: 1, CornerRadius: 8}, StyleFunc: func() core.Style { return core.Style{BorderColor: nswidgets.ColorsOf(cx).Line()} }, Children: nodes}
	})
}
func (d *desktopUsage) readingView(cx *core.Scope, r quota.Reading, now time.Time) core.Node {
	var nodes []core.Node
	if len(r.Windows) == 0 && r.Resets == nil {
		title, message := tr("No usage data"), tr("Refresh to read local accounts.")
		if r.Error != "" {
			title, message = tr("Failed to load"), r.Error
		}
		if d.busy {
			title, message = tr("Loading"), tr("Checking ")+quota.AgentName(r.Agent)+tr(" account.")
		}
		nodes = append(nodes, core.Text{TestID: "usage-empty", Content: title, Style: core.Style{FontSize: 13, FontWeight: ui.Semibold}}, settingsText(cx, func() string { return message }))
	}
	for i, w := range r.Windows {
		nodes = append(nodes, usageWindow(cx, w, now, fmt.Sprintf("usage-window-%s-%d", r.Agent, i)))
	}
	if agentmeta.UsageFor(r.Agent).UsageReset {
		nodes = append(nodes, d.resetSection(cx, r))
	}
	return settingsColumn(nodes...)
}
func usageWindow(cx *core.Scope, w quota.Window, now time.Time, id string) core.Node {
	colors := nswidgets.ColorsOf(cx)
	used := quota.Percent(w.Used)
	elapsed, timed := quota.TimeProgress(w, now)
	ink := func() core.Color {
		if quota.RemainingUsage(w.Used) == 0 {
			return colors.Danger()
		}
		if quota.RemainingUsage(w.Used) <= 20 {
			return colors.WarnInk()
		}
		return colors.Ink()
	}
	nodes := []core.Node{settingsActions(core.Text{Content: tr(w.Name), Style: core.Style{FlexGrow: 1, FontSize: 12}}, core.Text{Content: fmt.Sprintf("%.0f%%", used), Label: fmt.Sprintf(tr("%.0f%% used"), used), Style: core.Style{FontSize: 26, FontWeight: ui.Semibold}, StyleFunc: func() core.Style { return core.Style{Color: ink()} }}, settingsText(cx, func() string { return tr("Used") }))}
	bar := []core.Node{core.View{Style: core.Style{Width: core.Pct(float32(used)), Height: core.Pt(6), CornerRadius: 3}, StyleFunc: func() core.Style { return core.Style{Background: ink()} }}}
	if timed {
		bar = append(bar, core.View{TestID: id + "-time", Label: tr("Time progress ") + w.Name, Style: core.Style{Position: core.Absolute, Left: core.Pct(float32(elapsed)), Top: core.Pt(0), Width: core.Pt(2), Height: core.Pt(6)}, StyleFunc: func() core.Style {
			return core.Style{Background: colors.Ink(), BorderColor: colors.Canvas(), BorderWidth: 0.5}
		}})
	}
	nodes = append(nodes, core.View{TestID: id + "-progress", Label: tr("Quota progress ") + w.Name, Style: core.Style{Width: core.Pct(100), Height: core.Pt(6), CornerRadius: 3, Overflow: core.Hidden}, StyleFunc: func() core.Style { return core.Style{Background: colors.Line()} }, Children: bar})
	if timed {
		nodes = append(nodes, ui.Tooltip{Text: tr("Reference usage at a steady pace over the billing cycle, not the actual request rate."), Trigger: settingsText(cx, func() string { return usagePaceText(used, elapsed) })})
	}
	reset := w.Reset
	if reset == "" && w.ResetsAt > 0 {
		reset = time.Unix(w.ResetsAt, 0).Local().Format("01-02 15:04")
	}
	if reset != "" {
		nodes = append(nodes, settingsText(cx, func() string { return reset + tr(" reset") }))
	}
	return core.View{TestID: id, Style: core.Style{Gap: 6}, Children: nodes}
}
func usagePaceText(used, elapsed float64) string {
	pace, delta := int(math.Round(elapsed)), int(math.Round(elapsed-used))
	if delta > 0 {
		return fmt.Sprintf(tr("Steady pace %d%% · %d points under"), pace, delta)
	}
	if delta < 0 {
		return fmt.Sprintf(tr("Steady pace %d%% · %d points over"), pace, -delta)
	}
	return fmt.Sprintf(tr("Steady pace %d%% · On pace"), pace)
}
func usagePanelStatus(r quota.Reading, busy bool, now time.Time) string {
	if busy {
		return tr("Updating")
	}
	if r.At.IsZero() {
		return tr("Not updated yet")
	}
	action := tr("Updated")
	if r.Error != "" || len(r.Windows) == 0 {
		action = tr("Checked")
	}
	if now.Sub(r.At) < time.Minute {
		if r.Error != "" || len(r.Windows) == 0 {
			return tr("Checked just now")
		}
		return tr("Updated just now")
	}
	format := "15:04"
	if y, m, d := r.At.Local().Date(); y != now.Year() || m != now.Month() || d != now.Day() {
		format = "01-02 15:04"
	}
	return r.At.Local().Format(format) + " " + action
}
