package nsui

import (
	"fmt"
	"math"
	"time"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"
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

const (
	usagePanelWidth  core.Points = 320
	usagePanelHeader core.Points = 54
	usagePanelFooter core.Points = 44
	usagePanelLimit  core.Points = 480
)

func (d *desktopUsage) panelHeight(r quota.Reading, confirming bool) core.Points {
	if confirming {
		return 304
	}
	height := usagePanelHeader + usagePanelFooter + 32
	if len(r.Windows) == 0 && r.Resets == nil {
		height += 72
	}
	for i, w := range r.Windows[:min(3, len(r.Windows))] {
		if i > 0 {
			height += 22
		}
		height += 42
		if w.Reset != "" || w.ResetsAt > 0 {
			height += 22
		}
	}
	if agentmeta.UsageFor(r.Agent).UsageReset && (r.Resets != nil || d.resetMessage != "") {
		height += 96
	}
	return min(usagePanelLimit, height)
}

func (d *desktopUsage) panelView(cx *core.Scope) core.Node {
	now := usageClock(cx)
	colors := nswidgets.ColorsOf(cx)
	cx.Effect(func() {
		appearance := Live(func() string { return prefs.NormalizeAppearance(prefs.Load("appearance")) })()
		cx.Untrack(func() { setSettingsAppearance(cx, appearance) })
	})
	cx.OnKey("escape", func() { d.panel.Hide(); d.resetOpen = false; d.changed() })
	return core.Dynamic(func(cx *core.Scope) core.Node {
		d.revision.Get()
		Live(func() string { return prefs.Load("interfaceLanguage") })()
		tabs := []core.Node{}
		for _, agent := range usageAgents() {
			selected := d.panelAgent == agent
			name := quota.AgentName(agent)
			tabs = append(tabs, base.Button{TestID: "usage-tab-" + agent, Label: tr("View ") + name + tr(" usage"),
				Selected: func() bool { return selected }, Role: core.RoleTab,
				OnPress: func(core.PressEvent) { d.panelAgent = agent; d.resetOpen = false; d.changed() },
				Style:   core.Style{Direction: core.Row, Height: core.Pt(30), Padding: core.Symmetric(0, 10), Gap: 6, AlignItems: core.AlignCenter, CornerRadius: 6},
				StyleFunc: func() core.Style {
					return core.Style{Hover: &core.Style{Background: colors.Bubble()}, Selected: &core.Style{Background: colors.Hover()}, FocusVisible: ui.FocusRing(cx, -2)}
				},
				Children: []core.Node{nswidgets.AgentIcon(agentmeta.UsageFor(agent).ID, 14),
					core.Text{Content: name, Style: core.Style{FontSize: 12, FontWeight: 500},
						StyleFunc: func() core.Style {
							color := colors.Muted()
							if selected {
								color = colors.Ink()
							}
							return core.Style{Color: color}
						}},
				}})
		}
		r := d.reading(d.panelAgent)
		confirming := d.resetOpen && d.reset != nil && d.reset.Agent == r.Agent
		var body core.Node
		if confirming {
			body = d.resetConfirmation(cx)
		} else {
			body = d.readingView(cx, r, now(), true)
		}
		var footer []core.Node
		if r.Plan != "" {
			footer = append(footer, ui.Tooltip{Text: r.Plan, Trigger: core.Text{Content: r.Plan, MaxLines: 1,
				Style:     core.Style{FontSize: 10, FontWeight: 500, Padding: core.Symmetric(3, 6), CornerRadius: 4, MaxWidth: core.Pt(84)},
				StyleFunc: func() core.Style { return core.Style{Background: colors.Hover(), Color: colors.Muted()} }}})
		}
		footer = append(footer, core.Text{TestID: "usage-status", Content: usagePanelStatus(r, d.busy, now()), MaxLines: 1,
			Style:     core.Style{FontSize: 11, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)},
			StyleFunc: func() core.Style { return core.Style{Color: colors.Muted()} }},
			usageIconButton(cx, "usage-refresh", "refresh", tr("Refresh usage"), d.refresh, func() bool { return d.busy }))
		return core.View{TestID: "usage-panel", Label: tr("Usage panel"),
			Style: core.Style{Width: core.Pt(usagePanelWidth), Height: core.Pt(d.panelHeight(r, confirming))},
			StyleFunc: func() core.Style {
				if desktop.ShowsMaterial(cx) {
					return core.Style{}
				}
				return core.Style{Background: colors.Canvas(), CornerRadius: 12, BorderWidth: 1, BorderColor: colors.Line(), Overflow: core.Hidden}
			},
			Children: []core.Node{
				core.View{TestID: "usage-header", Style: core.Style{Direction: core.Row, Height: core.Pt(usagePanelHeader), Padding: core.Symmetric(10, 12), AlignItems: core.AlignCenter, Gap: 6}, Children: []core.Node{
					core.View{Role: core.RoleTabList, Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4}, Children: tabs},
					core.View{Style: core.Style{FlexGrow: 1}},
					usageIconButton(cx, "usage-account", "settings", tr("Account"), func() { d.showSettings("account") }, nil),
				}},
				core.Scroll{TestID: "usage-body", Style: ui.FillRest, Children: []core.Node{
					core.View{Style: core.Style{Padding: core.Edges{Top: 12, Right: 18, Bottom: 20, Left: 18}}, Children: []core.Node{body}},
				}},
				usageDivider(cx),
				core.View{TestID: "usage-footer", Style: core.Style{Direction: core.Row, Height: core.Pt(usagePanelFooter), Padding: core.Symmetric(0, 16), AlignItems: core.AlignCenter, Gap: 8}, Children: footer},
			}}
	})
}

func usageIconButton(cx *core.Scope, id, icon, label string, press func(), disabled func() bool) core.Node {
	return ui.IconButton{TestID: id, Icon: nswidgets.IconOf(icon), Label: label, OnPress: press, Disabled: disabled,
		Size: 28, IconSize: 15, Tint: nswidgets.ColorsOf(cx).Muted}
}

func usageDivider(cx *core.Scope) core.Node {
	return core.View{Style: core.Style{Height: core.Pt(0.5), FlexShrink: 0}, StyleFunc: func() core.Style {
		return core.Style{Background: nswidgets.ColorsOf(cx).Line()}
	}}
}

func (d *desktopUsage) subscriptionCard(cx *core.Scope, agent string) core.Node {
	now := usageClock(cx)
	return core.Dynamic(func(cx *core.Scope) core.Node {
		r := d.reading(agent)
		title := quota.AgentName(agent)
		if r.Plan != "" {
			title += " · " + r.Plan
		}
		nodes := []core.Node{core.Text{Content: title, Style: core.Style{FontSize: 14, FontWeight: ui.Semibold}}}
		if d.resetOpen && d.reset != nil && d.reset.Agent == agent {
			nodes = append(nodes, d.resetConfirmation(cx))
		} else {
			nodes = append(nodes, d.readingView(cx, r, now(), false))
		}
		nodes = append(nodes, settingsText(cx, func() string { return usagePanelStatus(r, d.busy, now()) }))
		return core.View{TestID: "usage-card-" + agent, Style: core.Style{Gap: 14, Padding: core.All(14), BorderWidth: 1, CornerRadius: 8}, StyleFunc: func() core.Style { return core.Style{BorderColor: nswidgets.ColorsOf(cx).Line()} }, Children: nodes}
	})
}
func (d *desktopUsage) readingView(cx *core.Scope, r quota.Reading, now time.Time, compact bool) core.Node {
	var nodes []core.Node
	if len(r.Windows) == 0 && r.Resets == nil {
		title, message := tr("No usage data"), tr("Refresh to read local accounts.")
		if d.c.RemoteHost() {
			message = tr("Refresh subscription quota")
		}
		if r.Error != "" {
			title, message = tr("Failed to load"), r.Error
		}
		if d.busy {
			title, message = tr("Loading"), tr("Checking ")+quota.AgentName(r.Agent)+tr(" account.")
		}
		heading := []core.Node{core.Text{TestID: "usage-empty", Content: title, Style: core.Style{FontSize: 13, FontWeight: 500}}}
		if d.busy {
			heading = append([]core.Node{ui.Spinner{Size: 14, Tint: nswidgets.ColorsOf(cx).Muted}}, heading...)
		}
		nodes = append(nodes, core.View{Style: core.Style{Gap: 8}, Children: []core.Node{
			settingsActions(heading...), nswidgets.MutedText(cx, func() string { return message }),
		}})
	}
	for i, w := range r.Windows {
		nodes = append(nodes, usageWindow(cx, w, now, fmt.Sprintf("usage-window-%s-%d", r.Agent, i)))
	}
	if agentmeta.UsageFor(r.Agent).UsageReset {
		if r.Resets != nil || d.resetMessage != "" {
			nodes = append(nodes, d.resetSection(cx, r, compact))
		}
	}
	gap := core.Points(10)
	if compact {
		gap = 22
	}
	return core.View{Style: core.Style{Gap: gap, MinWidth: core.Pt(0)}, Children: nodes}
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
	nodes := []core.Node{core.View{Style: core.Style{Direction: core.Row, Height: core.Pt(30), Gap: 6, AlignItems: core.AlignCenter}, Children: []core.Node{
		core.Text{Content: tr(w.Name), MaxLines: 1, Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), FontSize: 12},
			StyleFunc: func() core.Style { return core.Style{Color: colors.Muted()} }},
		core.Text{Content: fmt.Sprintf("%.0f%%", used), Label: fmt.Sprintf(tr("%.0f%% used"), used),
			Style: core.Style{FlexShrink: 0, FontSize: 26, FontWeight: ui.Semibold}, StyleFunc: func() core.Style { return core.Style{Color: ink()} }},
		core.Text{Content: tr("Used"), Style: core.Style{FlexShrink: 0, FontSize: 11},
			StyleFunc: func() core.Style { return core.Style{Color: colors.Muted()} }},
	}}}
	bar := []core.Node{core.View{Style: core.Style{Width: core.Pct(float32(used)), Height: core.Pt(6), CornerRadius: 3}, StyleFunc: func() core.Style { return core.Style{Background: ink()} }}}
	if timed {
		bar = append(bar, core.View{TestID: id + "-time", Label: tr("Time progress ") + w.Name, Style: core.Style{Position: core.Absolute, Left: core.Pct(float32(elapsed)), Top: core.Pt(0), Width: core.Pt(2), Height: core.Pt(6)}, StyleFunc: func() core.Style {
			return core.Style{Background: colors.Ink(), BorderColor: colors.Canvas(), BorderWidth: 0.5}
		}})
	}
	nodes = append(nodes, core.View{TestID: id + "-progress", Label: tr("Quota progress ") + w.Name, Style: core.Style{Width: core.Pct(100), Height: core.Pt(6), CornerRadius: 3, Overflow: core.Hidden}, StyleFunc: func() core.Style { return core.Style{Background: colors.Line()} }, Children: bar})
	reset := w.Reset
	if reset == "" && w.ResetsAt > 0 {
		reset = time.Unix(w.ResetsAt, 0).Local().Format("01-02 15:04")
	}
	if timed || reset != "" {
		var detail []core.Node
		if timed {
			detail = append(detail, ui.Tooltip{Text: tr("Reference usage at a steady pace over the billing cycle, not the actual request rate."),
				Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)},
				Trigger: core.Text{TestID: id + "-pace", Content: usagePaceText(used, elapsed), MaxLines: 1,
					Style: core.Style{FontSize: 10}, StyleFunc: func() core.Style { return core.Style{Color: colors.Muted()} }}})
		}
		if reset != "" {
			text := reset + tr(" reset")
			detail = append(detail, ui.Tooltip{Text: text, Trigger: core.Text{TestID: id + "-reset", Content: text, MaxLines: 1,
				Style: core.Style{FontSize: 10, FlexShrink: 0}, StyleFunc: func() core.Style { return core.Style{Color: colors.Muted()} }}})
		}
		nodes = append(nodes, core.View{Style: core.Style{Direction: core.Row, Height: core.Pt(16), Gap: 6, AlignItems: core.AlignCenter}, Children: detail})
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
