package mobile

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/phoneapi"
	"miya/internal/widgets"
)

func quotaResetLabel(unix int64) string {
	if unix <= 0 {
		return ""
	}
	t := time.Unix(unix, 0).Local()
	days := [...]string{tr("Sun"), tr("Mon"), tr("Tue"), tr("Wed"), tr("Thu"), tr("Fri"), tr("Sat")}
	return days[t.Weekday()] + t.Format(" 15:04")
}

func (m *App) drawerQuotas(c *ui.Context) {
	var quotas []phoneapi.AgentQuota
	for _, q := range m.State.Quotas {
		if len(q.Windows) > 0 {
			quotas = append(quotas, q)
		}
	}
	if len(quotas) == 0 {
		m.QuotaOpen = false
		return
	}
	p := m.Pal
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		for _, q := range quotas {
			name := m.agentDescriptor(q.Agent).Name
			cc := c.Key(q.Agent)
			button := m.phoneButton(cc).MinHeight(44).Padding(0).Gap(5).Radius(12).Label(name + tr(" quota"))
			button.Children(func() {
				track, fill := "#4a4a50", "#dedee3"
				if !m.Dark {
					track, fill = "#dddde2", "#505058"
				}
				remaining := math.Max(0, math.Min(100, 100-q.Windows[0].UsedPercent))
				size := m.Font(28)
				ui.Box(cc).Size(size, size).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					ui.Image(cc.Key("track"), widgets.RingSVG(100, track)).Size(size, size).Absolute().Left(0).Top(0)
					ui.Image(cc.Key("remaining"), widgets.RingSVG(remaining, fill)).Size(size, size).Absolute().Left(0).Top(0)
					ui.Text(cc, fmt.Sprintf("%.0f", remaining)).FontSize(m.Font(10)).FontWeight(600).TextColor(p.ink)
				})
				ui.Text(cc, name).FontSize(m.Font(16)).FontWeight(500).TextColor(p.ink)
			})
			if button.Clicked() {
				m.QuotaOpen = !m.QuotaOpen || m.quotaAgent != q.Agent
				m.quotaAgent = q.Agent
			}
			if m.quotaAgent != q.Agent {
				continue
			}
			ui.PopoverBase(cc, button, &m.QuotaOpen, func(tip ui.Element) {
				tip.AttachTo(button, ui.AnchorTopStart, ui.AnchorBottomStart).Margin(0, 0, 8, 0).
					Width(280).Padding(14, 16).Gap(12).Radius(22).Background(p.surface).Border(1, p.line).
					Shadow(0, 4, 16, 0, ui.RGBA(0, 0, 0, 0.2)).TextColor(p.ink)
				for i, w := range q.Windows {
					ui.Row(cc.Key(i)).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(cc, tr(w.Label)).FontSize(m.Font(13)).TextColor(p.muted)
						ui.Text(cc, fmt.Sprintf(tr("%.0f%% used"), w.UsedPercent)).FontSize(m.Font(13)).FontWeight(600).Grow(1)
						if reset := quotaResetLabel(w.ResetsAt); reset != "" {
							ui.Text(cc, reset).FontSize(m.Font(12)).TextColor(p.muted)
						}
					})
				}
			})
		}
	})
}
