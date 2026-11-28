package nsmobile

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/nswidgets"
	phoneproto "miya/internal/phone/protocol"
)

func (m *App) drawerQuotas(cx *core.Scope) core.Node {
	open := core.NewSignal(cx, "")
	quotas := core.NewMemoFunc(cx, live(m, func() []phoneproto.AgentQuota {
		var rows []phoneproto.AgentQuota
		for _, quota := range m.State.Quotas {
			if len(quota.Windows) > 0 {
				rows = append(rows, quota)
			}
		}
		return rows
	}), func(a, b []phoneproto.AgentQuota) bool {
		return slices.EqualFunc(a, b, func(a, b phoneproto.AgentQuota) bool {
			return a.Agent == b.Agent && slices.Equal(a.Windows, b.Windows)
		})
	})
	cx.Effect(func() {
		id := open.Get()
		if id != "" && (!m.drawerOpen.Get() || !slices.ContainsFunc(quotas.Get(), func(q phoneproto.AgentQuota) bool { return q.Agent == id })) {
			open.Set("")
		}
	})
	return core.Show{When: func() bool { return len(quotas.Get()) > 0 }, Then: func(*core.Scope) core.Node {
		return core.View{TestID: "drawer-quotas",
			Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Wrap: core.Wrap, Gap: 8, FlexShrink: 0, MinWidth: core.Pt(0)},
			Children: []core.Node{core.Each(quotas.Get, func(q phoneproto.AgentQuota) string { return q.Agent },
				func(cx *core.Scope, quota func() phoneproto.AgentQuota, _ func() int) core.Node {
					return m.drawerQuota(cx, quota, open)
				})}}
	}}
}

func (m *App) drawerQuota(cx *core.Scope, quota func() phoneproto.AgentQuota, open *core.Signal[string]) core.Node {
	const ringSize core.Points = 28
	id := quota().Agent
	anchor := new(core.Ref)
	colors := nswidgets.ColorsOf(cx)
	name := core.NewMemo(cx, live(m, func() string { return m.agentName(id) }))
	label := func() string { return name.Get() + tr(" quota") }
	remaining := core.NewMemo(cx, func() int {
		return int(math.Round(min(max(100-quota().Windows[0].UsedPercent, 0), 100)))
	})
	shown := func() bool { return open.Get() == id && m.drawerOpen.Get() }
	close := func() {
		if open.GetUntracked() == id {
			open.Set("")
		}
	}
	return core.Group{
		core.View{Ref: anchor, TestID: "drawer-quota-" + id, Role: core.RoleButton,
			LabelFunc: func() string { return fmt.Sprintf(tr("%s · %d%% remaining"), name.Get(), remaining.Get()) },
			Expanded:  shown, ShowsExpanded: true,
			OnPress: func(core.PressEvent) {
				if open.GetUntracked() == id {
					close()
				} else {
					open.Set(id)
				}
			},
			Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 5, CornerRadius: 12,
				MinHeight: core.Pt(44), MinWidth: core.Pt(0), MaxWidth: core.Pct(100), FlexShrink: 1},
			StyleFunc: func() core.Style { return core.Style{Active: &core.Style{Background: colors.Hover()}} },
			Children: []core.Node{
				core.View{AccessibilityElementsHidden: func() bool { return true },
					Style: core.Style{Width: core.Pt(ringSize), Height: core.Pt(ringSize), FlexShrink: 0,
						AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
					Children: []core.Node{
						ui.ProgressRing{Size: 22, LineWidth: 3, Value: func() float32 { return float32(remaining.Get()) }, Tint: func() core.Color {
							if cx.Env().Dark() {
								return core.RGB(0xdedee3)
							}
							return core.RGB(0x505058)
						}},
						core.View{PointerEvents: core.PointerEventsNone,
							Style: core.Style{Position: core.Absolute, Top: core.Pt(0), Left: core.Pt(0), Width: core.Pct(100), Height: core.Pct(100),
								AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
							Children: []core.Node{core.Text{TestID: "drawer-quota-remaining-" + id, NoFontScaling: true,
								ContentFunc: func() string { return fmt.Sprint(remaining.Get()) },
								Style:       core.Style{FontSize: 10, FontWeight: 600}}}},
					}},
				core.Text{ContentFunc: name.Get, MaxLines: 1, Style: core.Style{FontSize: 16, FontWeight: 500, MinWidth: core.Pt(0), FlexShrink: 1}},
			}},
		core.Show{When: shown, Then: func(cx *core.Scope) core.Node {
			return core.Overlay{TestID: "drawer-quota-details", Role: core.RoleDialog, LabelFunc: label,
				Anchor: anchor, Placement: core.PlaceBottom, Align: core.AlignStart, Offset: 8, Margin: 8,
				OnCancel: close, OnPressOutside: func(core.Point) { close() },
				Style: core.Style{Padding: core.Symmetric(14, 16), Gap: 12, CornerRadius: 22, BorderWidth: 1},
				StyleFunc: func() core.Style {
					return core.Style{Width: core.Pt(min(280, max(0, cx.Env().Window().Width-16))),
						Background: colors.Bubble(), Color: colors.Ink(), BorderColor: colors.Line(),
						ShadowColor: core.RGBA(0x000000, 0.2), ShadowOffset: core.Point{Y: 4}, ShadowRadius: 8}
				},
				Children: []core.Node{core.Dynamic(func(cx *core.Scope) core.Node {
					var rows []core.Node
					for _, w := range quota().Windows {
						row := []core.Node{
							core.Text{Content: tr(w.Label), StyleFunc: func() core.Style {
								return core.Style{FontSize: 13, Color: colors.Muted(), FlexShrink: 1}
							}},
							core.Text{Content: fmt.Sprintf(tr("%.0f%% used"), w.UsedPercent),
								Style: core.Style{FontSize: 13, FontWeight: 600, FlexGrow: 1, FlexShrink: 1}},
						}
						if reset := quotaResetLabel(w.ResetsAt); reset != "" {
							row = append(row, core.Text{Content: reset, StyleFunc: func() core.Style {
								return core.Style{FontSize: 12, Color: colors.Muted(), FlexShrink: 1}
							}})
						}
						rows = append(rows, core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10}, Children: row})
					}
					return core.View{Style: core.Style{Gap: 12}, Children: rows}
				})}}
		}},
	}
}

func quotaResetLabel(unix int64) string {
	if unix <= 0 {
		return ""
	}
	t := time.Unix(unix, 0).Local()
	days := [...]string{tr("Sun"), tr("Mon"), tr("Tue"), tr("Wed"), tr("Thu"), tr("Fri"), tr("Sat")}
	return days[t.Weekday()] + t.Format(" 15:04")
}
