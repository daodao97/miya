package main

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

func quotaResetLabel(unix int64) string {
	if unix <= 0 {
		return ""
	}
	t := time.Unix(unix, 0).Local()
	days := [...]string{tr("Sun"), tr("Mon"), tr("Tue"), tr("Wed"), tr("Thu"), tr("Fri"), tr("Sat")}
	return days[t.Weekday()] + t.Format(" 15:04")
}

func (m *mobileApp) drawerQuotas(c *ui.Context) {
	var quotas []phoneAgentQuota
	for _, q := range m.state.Quotas {
		if len(q.Windows) > 0 {
			quotas = append(quotas, q)
		}
	}
	if len(quotas) == 0 {
		m.quotaOpen = false
		return
	}
	p := m.pal
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		for _, q := range quotas {
			name := "Codex"
			if q.Agent == agentClaude {
				name = "Claude"
			}
			cc := c.Key(q.Agent)
			button := m.phoneButton(cc).MinHeight(44).Padding(0).Gap(5).Radius(12).Label(name + tr(" quota"))
			button.Children(func() {
				track, fill := "#4a4a50", "#dedee3"
				if !m.dark {
					track, fill = "#dddde2", "#505058"
				}
				remaining := math.Max(0, math.Min(100, 100-q.Windows[0].UsedPercent))
				size := m.font(28)
				ui.Box(cc).Size(size, size).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					ui.Image(cc.Key("track"), ringSVG(100, track)).Size(size, size).Absolute().Left(0).Top(0)
					ui.Image(cc.Key("remaining"), ringSVG(remaining, fill)).Size(size, size).Absolute().Left(0).Top(0)
					ui.Text(cc, fmt.Sprintf("%.0f", remaining)).FontSize(m.font(10)).FontWeight(600).TextColor(p.ink)
				})
				ui.Text(cc, name).FontSize(m.font(16)).FontWeight(500).TextColor(p.ink)
			})
			if button.Clicked() {
				m.quotaOpen = !m.quotaOpen || m.quotaAgent != q.Agent
				m.quotaAgent = q.Agent
			}
			if m.quotaAgent != q.Agent {
				continue
			}
			ui.PopoverBase(cc, button, &m.quotaOpen, func(tip ui.Element) {
				tip.AttachTo(button, ui.AnchorTopStart, ui.AnchorBottomStart).Margin(0, 0, 8, 0).
					Width(280).Padding(14, 16).Gap(12).Radius(22).Background(p.surface).Border(1, p.line).
					Shadow(0, 4, 16, 0, ui.RGBA(0, 0, 0, 0.2)).TextColor(p.ink)
				for i, w := range q.Windows {
					ui.Row(cc.Key(i)).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(cc, tr(w.Label)).FontSize(m.font(13)).TextColor(p.muted)
						ui.Text(cc, fmt.Sprintf(tr("%.0f%% used"), w.UsedPercent)).FontSize(m.font(13)).FontWeight(600).Grow(1)
						if reset := quotaResetLabel(w.ResetsAt); reset != "" {
							ui.Text(cc, reset).FontSize(m.font(12)).TextColor(p.muted)
						}
					})
				}
			})
		}
	})
}
