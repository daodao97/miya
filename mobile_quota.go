package main

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// Only subscription usage is shared, never account identity or credentials.
// Agents without real quota data are omitted entirely.
type phoneAgentQuota struct {
	Agent   string             `json:"agent"`
	Windows []phoneQuotaWindow `json:"windows"`
}

type phoneQuotaWindow struct {
	Label       string  `json:"label"`
	UsedPercent float64 `json:"usedPercent"`
	ResetsAt    int64   `json:"resetsAt,omitempty"`
}

func (c *chatController) phoneQuotas() []phoneAgentQuota {
	q := c.quotaLimits.RateLimits
	if v, ok := c.quotaLimits.ByID[codexAccountLimitID]; ok {
		q = &v
	}
	if q == nil {
		return nil
	}
	windows := composerQuotaWindows(c.quotaLimits)
	// Preserve additional account windows without using model-specific buckets.
	for _, w := range []*quotaWindow{q.Primary, q.Secondary} {
		if w == nil {
			continue
		}
		found := false
		for _, existing := range windows {
			if existing == w {
				found = true
			}
		}
		if !found {
			windows = append(windows, w)
		}
	}
	quota := phoneAgentQuota{Agent: agentCodex}
	for i, w := range windows {
		if math.IsNaN(w.UsedPercent) || math.IsInf(w.UsedPercent, 0) {
			continue
		}
		v := phoneQuotaWindow{Label: strings.TrimSuffix(quotaLabel(w, i), "额度"), UsedPercent: math.Max(0, math.Min(100, w.UsedPercent))}
		if w.ResetsAt != nil && *w.ResetsAt > 0 {
			v.ResetsAt = *w.ResetsAt
		}
		quota.Windows = append(quota.Windows, v)
	}
	if len(quota.Windows) == 0 {
		return nil
	}
	return []phoneAgentQuota{quota}
}

func quotaResetLabel(unix int64) string {
	if unix <= 0 {
		return ""
	}
	t := time.Unix(unix, 0).Local()
	days := [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
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
			button := quiet(cc).Height(44).Padding(0, 8).Gap(6).Radius(12).Label(name + " 额度")
			button.Children(func() {
				track, fill := "#4a4a50", "#dedee3"
				if !m.dark {
					track, fill = "#dddde2", "#505058"
				}
				ui.Box(cc).Size(24, 24).Children(func() {
					ui.Image(cc.Key("track"), ringSVG(100, track)).Size(24, 24).Absolute().Left(0).Top(0)
					ui.Image(cc.Key("used"), ringSVG(q.Windows[0].UsedPercent, fill)).Size(24, 24).Absolute().Left(0).Top(0)
				})
				ui.Text(cc, name).FontSize(16).FontWeight(500).TextColor(p.ink)
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
						ui.Text(cc, w.Label).FontSize(13).TextColor(p.muted)
						ui.Text(cc, fmt.Sprintf("已用 %.0f%%", w.UsedPercent)).FontSize(13).FontWeight(600).Grow(1)
						if reset := quotaResetLabel(w.ResetsAt); reset != "" {
							ui.Text(cc, reset).FontSize(12).TextColor(p.muted)
						}
					})
				}
			})
		}
	})
}
