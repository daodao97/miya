package main

import (
	"fmt"
	"runtime"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"miya/internal/agentusage"
)

const usagePanelWidth, usagePanelHeader, usagePanelFooter = 320, 54, 44

func (d *desktopUsage) reading(agent string) agentusage.Reading {
	for _, r := range d.readings {
		if r.Agent == agent {
			return r
		}
	}
	return agentusage.Reading{Agent: agent}
}

func (d *desktopUsage) height() int {
	if d.panelAgent == "codex" && d.resetOpen {
		return 304
	}
	r := d.reading(d.panelAgent)
	height := usagePanelHeader + usagePanelFooter + 32
	if len(r.Windows) == 0 {
		height += 72
	}
	for i, w := range r.Windows[:min(3, len(r.Windows))] {
		if i > 0 {
			height += 22
		}
		height += 40
		if w.Reset != "" {
			height += 22
		}
	}
	if d.panelAgent == "codex" && (r.Resets != nil || d.resetMessage != "") {
		height += 96
	}
	return min(480, height)
}

func (app *chatApp) closeUsagePanel() {
	if d := app.usage; d != nil && d.panel != nil {
		d.panel.Close()
	}
}

func (app *chatApp) toggleUsagePanel() {
	d := app.usage
	if d == nil || d.closed || d.tray == nil {
		return
	}
	if d.panel != nil {
		app.closeUsagePanel()
		return
	}
	if d.panelAgent == "" {
		d.panelAgent = d.prefs.agent
		if r := selectedUsage(d.readings, d.prefs.agent); r != nil {
			d.panelAgent = r.Agent
		}
		if d.panelAgent == "" {
			d.panelAgent = "codex"
		}
	}
	d.panelHeight = d.height()
	panel := mygo.NewFlyout(mygo.FlyoutOptions{
		Tray: d.tray, Placement: mygo.PlacementBottom, Gap: 6,
		Width: usagePanelWidth, Height: d.panelHeight,
		Focusable: true, Popover: true, Shadow: true, Hidden: true,
		Content: ui.View(app.usagePanelView),
	})
	d.panel = panel
	panel.SetTitle("Miya 用量")
	panel.OnClosed(func() {
		if d.panel == panel {
			d.panel, d.resetOpen = nil, false
		}
	})
	panel.Show()
}

func (app *chatApp) invalidateUsageViews() {
	if w := currentWindow(); w != nil {
		w.Invalidate()
	}
	if d := app.usage; d != nil && d.panel != nil {
		if h := d.height(); h != d.panelHeight {
			d.panelHeight = h
			d.panel.SetContentSize(usagePanelWidth, h)
		}
		d.panel.Invalidate()
	}
}

// The tray has its own theme so drawing it cannot change the main window's
// shared palette. System material supplies the background on macOS.
func (app *chatApp) usageTheme(c *ui.Context) (*ui.Theme, desktopPalette) {
	dark := c.Theme().Dark
	if app.appearance != "" {
		dark = app.appearance == "dark"
	}
	t, p := *ui.LightTheme(), desktopLight
	if dark {
		t, p = *ui.DarkTheme(), desktopDark
		p.muted = ui.Hex("#9b9ba2")
	} else {
		p.muted = ui.Hex("#6d6d72")
	}
	t.Background, t.Surface, t.SurfaceHover, t.Border = p.canvas, p.subtle, p.hover, p.line
	t.Text, t.TextMuted, t.Danger = p.ink, p.muted, p.danger
	t.Accent, t.AccentText, t.FontSize = p.ink, p.canvas, 13
	return &t, p
}

func usageIconButton(c *ui.Context, glyph, label string) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Size(28, 28).Padding(0).Radius(6).AlignItems(ui.Center).Justify(ui.Center).Label(label).Tooltip(label)
	if b.Hovered() && !b.IsDisabled() {
		b.Background(t.SurfaceHover)
	}
	return b.Children(func() { icon(c, glyph, 15, t.TextMuted) })
}

func (app *chatApp) usagePanelView(c *ui.Context) {
	d := app.usage
	if d == nil {
		return
	}
	if d.panelAgent == "" {
		d.panelAgent = "codex"
	}
	t, p := app.usageTheme(c)
	c.SetTheme(t)
	c.Root().Background(ui.Transparent)
	c.After(time.Minute)
	box := ui.Column(c).Fill().TextColor(t.Text).FontSize(13).Label("用量面板")
	if !c.Vibrancy() {
		box.Background(t.Background)
		if runtime.GOOS != "windows" {
			box.Radius(12).Border(1, t.Border)
		}
	}
	box.Children(func() {
		ui.Row(c).FillWidth().Height(usagePanelHeader).Padding(10, 12).Gap(6).AlignItems(ui.Center).Children(func() {
			agents, selected := []string{"codex", "claude"}, 0
			if d.panelAgent == "claude" {
				selected = 1
			}
			tabs := ui.TabsBase(c, &selected, 2)
			tabs.List.Gap(4).Children(func() {
				for i, agent := range agents {
					tab := tabs.Tab(i).Height(30).Padding(0, 10).Gap(6).Radius(6).AlignItems(ui.Center).Label("查看 " + usageAgentName(agent) + " 用量")
					color := t.TextMuted
					if i == selected {
						tab.Background(t.SurfaceHover)
						color = t.Text
					} else if tab.Hovered() {
						tab.Background(t.Surface)
					}
					tab.Children(func() {
						glyph := "brand-openai"
						if agent == "claude" {
							glyph = "claude-code"
						}
						icon(c, glyph, 14, color)
						ui.Text(c, usageAgentName(agent)).FontSize(12).FontWeight(500).TextColor(color)
					})
				}
			})
			if tabs.List.Changed() {
				d.panelAgent = agents[selected]
				app.invalidateUsageViews()
			}
			ui.Box(c).Grow(1)
			if usageIconButton(c, "settings", "用量设置").Clicked() {
				app.openUsageSettingsFromPanel()
			}
		})
		r := d.reading(d.panelAgent)
		ui.Scroll(c.Key("usage-"+d.panelAgent)).FillWidth().Grow(1).MinHeight(0).Padding(12, 18, 20, 18).Gap(22).Children(func() {
			if d.panelAgent == "codex" && d.resetOpen {
				app.usageResetConfirmation(c, r)
				return
			}
			if len(r.Windows) == 0 && r.Resets == nil {
				usagePanelEmpty(c, r, d.busy)
			}
			for _, w := range r.Windows {
				usagePanelWindow(c, w, p)
			}
			if d.panelAgent == "codex" {
				app.usageResetSection(c, r, true)
			}
		})
		ui.Box(c).FillWidth().Height(0.5).Background(t.Border)
		ui.Row(c).FillWidth().Height(usagePanelFooter).Padding(0, 16).Gap(8).AlignItems(ui.Center).Children(func() {
			if r.Plan != "" {
				ui.Text(c, r.Plan).FontSize(10).FontWeight(500).TextColor(t.TextMuted).Padding(3, 6).Radius(4).Background(t.SurfaceHover).MaxWidth(84).SingleLine().Ellipsis("…").Tooltip(r.Plan)
			}
			ui.Text(c, usagePanelStatus(r, d.busy, c.Now())).FontSize(11).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			if usageIconButton(c, "refresh", "刷新用量").Disabled(d.busy).Clicked() {
				app.refreshUsage()
			}
		})
	})
}

func usagePanelWindow(c *ui.Context, w agentusage.Window, p desktopPalette) {
	color := p.ink
	if remainingUsage(w.Used) <= 20 {
		color = p.warnInk
	}
	if remainingUsage(w.Used) == 0 {
		color = p.danger
	}
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		ui.Row(c).FillWidth().Height(30).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, w.Name).FontSize(12).TextColor(p.muted).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").Tooltip(w.Name)
			ui.Text(c, fmt.Sprintf("%d%%", remainingUsage(w.Used))).Label(fmt.Sprintf("剩余 %d%%", remainingUsage(w.Used))).FontSize(26).FontWeight(600).TextColor(color).Shrink(0)
			ui.Text(c, "剩余").FontSize(11).TextColor(p.muted).Shrink(0)
		})
		ui.Box(c).FillWidth().Height(4).Radius(2).Clip().Background(p.line).Children(func() {
			ui.Box(c).WidthPercent(float32(100 - agentusage.Percent(w.Used))).FillHeight().Background(color).Radius(2)
		})
		if w.Reset != "" {
			ui.Text(c, w.Reset+" 重置").FontSize(11).TextColor(p.muted).Height(16).SingleLine().Ellipsis("…").Tooltip(w.Reset + " 重置")
		}
	})
}

func usagePanelEmpty(c *ui.Context, r agentusage.Reading, busy bool) {
	title, message := "暂无额度数据", "刷新以读取本机账号。"
	if r.Error != "" {
		title, message = "读取失败", r.Error
	}
	if busy {
		title, message = "读取中", "正在查询 "+usageAgentName(r.Agent)+" 账号。"
	}
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if busy {
				ui.Spinner(c).FontSize(14).TextColor(c.Theme().TextMuted)
			}
			ui.Text(c, title).FontSize(13).FontWeight(500)
		})
		ui.Text(c, message).FillWidth().FontSize(12).LineHeight(1.5).TextColor(c.Theme().TextMuted)
	})
}

func usagePanelStatus(r agentusage.Reading, busy bool, now time.Time) string {
	if busy {
		return "更新中"
	}
	if r.At.IsZero() {
		return "尚未更新"
	}
	action := "更新"
	if r.Error != "" || len(r.Windows) == 0 {
		action = "检查"
	}
	if now.Sub(r.At) < time.Minute {
		return "刚刚" + action
	}
	format := "15:04"
	if y, m, d := r.At.Local().Date(); y != now.Year() || m != now.Month() || d != now.Day() {
		format = "01-02 15:04"
	}
	return r.At.Local().Format(format) + " " + action
}
