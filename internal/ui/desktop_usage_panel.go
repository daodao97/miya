package desktopui

import (
	"fmt"
	"math"
	"runtime"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"miya/internal/agentusage"
	"miya/internal/uiruntime"
	"miya/internal/widgets"
)

const UsagePanelWidth, UsagePanelHeader, UsagePanelFooter = 320, 54, 44

func (d *DesktopUsage) reading(agent string) agentusage.Reading {
	for _, r := range d.Readings {
		if r.Agent == agent {
			return r
		}
	}
	return agentusage.Reading{Agent: agent}
}

func (d *DesktopUsage) Height() int {
	if d.PanelAgent == "codex" && d.ResetOpen {
		return 304
	}
	r := d.reading(d.PanelAgent)
	height := UsagePanelHeader + UsagePanelFooter + 32
	if len(r.Windows) == 0 {
		height += 72
	}
	for i, w := range r.Windows[:min(3, len(r.Windows))] {
		if i > 0 {
			height += 22
		}
		height += 42
		if w.Reset != "" || w.DurationMins > 0 && w.ResetsAt > 0 {
			height += 22
		}
	}
	if d.PanelAgent == "codex" && (r.Resets != nil || d.resetMessage != "") {
		height += 96
	}
	return min(480, height)
}

func (app *App) closeUsagePanel() {
	if d := app.Usage; d != nil && d.panel != nil {
		d.panel.Close()
	}
}

func (app *App) toggleUsagePanel() {
	d := app.Usage
	if d == nil || d.closed || d.tray == nil {
		return
	}
	if d.panel != nil {
		app.closeUsagePanel()
		return
	}
	if d.PanelAgent == "" {
		d.PanelAgent = d.Prefs.Agent
		if r := agentusage.SelectedUsage(d.Readings, d.Prefs.Agent); r != nil {
			d.PanelAgent = r.Agent
		}
		if d.PanelAgent == "" {
			d.PanelAgent = "codex"
		}
	}
	d.panelHeight = d.Height()
	panel := mygo.NewFlyout(mygo.FlyoutOptions{
		Tray: d.tray, Placement: mygo.PlacementBottom, Gap: 6,
		Width: UsagePanelWidth, Height: d.panelHeight,
		Focusable: true, Popover: true, Shadow: true, Hidden: true,
		Content: ui.View(app.UsagePanelView),
	})
	d.panel = panel
	panel.SetTitle(tr("Miya Usage"))
	panel.OnClosed(func() {
		if d.panel == panel {
			d.panel, d.ResetOpen = nil, false
		}
	})
	panel.Show()
}

func (app *App) invalidateUsageViews() {
	if w := uiruntime.CurrentWindow(); w != nil {
		w.Invalidate()
	}
	if d := app.Usage; d != nil && d.panel != nil {
		if h := d.Height(); h != d.panelHeight {
			d.panelHeight = h
			d.panel.SetContentSize(UsagePanelWidth, h)
		}
		d.panel.Invalidate()
	}
}

// The tray has its own theme so drawing it cannot change the main window's
// shared palette. System material supplies the background on macOS.
func (app *App) usageTheme(c *ui.Context) (*ui.Theme, widgets.DesktopPalette) {
	dark := c.Theme().Dark
	if app.Appearance != "" {
		dark = app.Appearance == "dark"
	}
	t, p := *ui.LightTheme(), widgets.DesktopLight
	if dark {
		t, p = *ui.DarkTheme(), widgets.DesktopDark
		p.Muted = ui.Hex("#9b9ba2")
	} else {
		p.Muted = ui.Hex("#6d6d72")
	}
	t.Background, t.Surface, t.SurfaceHover, t.Border = p.Canvas, p.Subtle, p.Hover, p.Line
	t.Text, t.TextMuted, t.Danger = p.Ink, p.Muted, p.Danger
	t.Accent, t.AccentText, t.FontSize = p.Ink, p.Canvas, 13
	return &t, p
}

func usageIconButton(c *ui.Context, glyph, label string) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Size(28, 28).Padding(0).Radius(6).AlignItems(ui.Center).Justify(ui.Center).Label(label).Tooltip(label)
	if b.Hovered() && !b.IsDisabled() {
		b.Background(t.SurfaceHover)
	}
	return b.Children(func() { widgets.Icon(c, glyph, 15, t.TextMuted) })
}

func (app *App) UsagePanelView(c *ui.Context) {
	widgets.UseInterfaceLanguage(c)
	d := app.Usage
	if d == nil {
		return
	}
	if d.PanelAgent == "" {
		d.PanelAgent = "codex"
	}
	t, p := app.usageTheme(c)
	c.SetTheme(t)
	c.Root().Background(ui.Transparent)
	c.After(time.Minute)
	box := ui.Column(c).Fill().TextColor(t.Text).FontSize(13).Label(tr("Usage panel"))
	if !c.Vibrancy() {
		box.Background(t.Background)
		if runtime.GOOS != "windows" {
			box.Radius(12).Border(1, t.Border)
		}
	}
	box.Children(func() {
		ui.Row(c).FillWidth().Height(UsagePanelHeader).Padding(10, 12).Gap(6).AlignItems(ui.Center).Children(func() {
			agents, selected := []string{"codex", "claude"}, 0
			if d.PanelAgent == "claude" {
				selected = 1
			}
			tabs := ui.TabsBase(c, &selected, 2)
			tabs.List.Gap(4).Children(func() {
				for i, agent := range agents {
					tab := tabs.Tab(i).Height(30).Padding(0, 10).Gap(6).Radius(6).AlignItems(ui.Center).Label(tr("View ") + agentusage.AgentName(agent) + tr(" usage"))
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
						widgets.Icon(c, glyph, 14, color)
						ui.Text(c, agentusage.AgentName(agent)).FontSize(12).FontWeight(500).TextColor(color)
					})
				}
			})
			if tabs.List.Changed() {
				d.PanelAgent = agents[selected]
				app.invalidateUsageViews()
			}
			ui.Box(c).Grow(1)
			if usageIconButton(c, "settings", tr("Usage settings")).Clicked() {
				app.openUsageSettingsFromPanel()
			}
		})
		r := d.reading(d.PanelAgent)
		ui.Scroll(c.Key("usage-"+d.PanelAgent)).FillWidth().Grow(1).MinHeight(0).Padding(12, 18, 20, 18).Gap(22).Children(func() {
			if d.PanelAgent == "codex" && d.ResetOpen {
				app.usageResetConfirmation(c, r)
				return
			}
			if len(r.Windows) == 0 && r.Resets == nil {
				usagePanelEmpty(c, r, d.Busy)
			}
			for _, w := range r.Windows {
				usagePanelWindow(c, w, p)
			}
			if d.PanelAgent == "codex" {
				app.usageResetSection(c, r, true)
			}
		})
		ui.Box(c).FillWidth().Height(0.5).Background(t.Border)
		ui.Row(c).FillWidth().Height(UsagePanelFooter).Padding(0, 16).Gap(8).AlignItems(ui.Center).Children(func() {
			if r.Plan != "" {
				ui.Text(c, r.Plan).FontSize(10).FontWeight(500).TextColor(t.TextMuted).Padding(3, 6).Radius(4).Background(t.SurfaceHover).MaxWidth(84).SingleLine().Ellipsis("…").Tooltip(r.Plan)
			}
			ui.Text(c, UsagePanelStatus(r, d.Busy, c.Now())).FontSize(11).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			if usageIconButton(c, "refresh", tr("Refresh usage")).Disabled(d.Busy).Clicked() {
				app.refreshUsage()
			}
		})
	})
}

func usagePanelWindow(c *ui.Context, w agentusage.Window, p widgets.DesktopPalette) {
	color := p.Ink
	if agentusage.RemainingUsage(w.Used) <= 20 {
		color = p.WarnInk
	}
	if agentusage.RemainingUsage(w.Used) == 0 {
		color = p.Danger
	}
	elapsed, timed := agentusage.TimeProgress(w, c.Now())
	used := agentusage.Percent(w.Used)
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		ui.Row(c).FillWidth().Height(30).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr(w.Name)).FontSize(12).TextColor(p.Muted).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").Tooltip(tr(w.Name))
			ui.Text(c, fmt.Sprintf("%.0f%%", used)).Label(fmt.Sprintf(tr("%.0f%% used"), used)).FontSize(26).FontWeight(600).TextColor(color).Shrink(0)
			ui.Text(c, tr("Used")).FontSize(11).TextColor(p.Muted).Shrink(0)
		})
		ui.Box(c).FillWidth().Height(6).Radius(3).Clip().Background(p.Line).Label(tr("Quota progress ") + w.Name).Children(func() {
			ui.Box(c).WidthPercent(float32(used)).FillHeight().Background(color).Radius(3)
			if timed {
				ui.Box(c).Absolute().Left(1).Right(1).Top(0).Height(6).Children(func() {
					ui.Box(c).Absolute().LeftPercent(float32(elapsed)).Top(0).Size(2, 6).Translate(-1, 0).Background(p.Ink).Shadow(0, 0, 0, 1, p.Canvas).
						Label(tr("Time progress ") + w.Name).Tooltip(fmt.Sprintf(tr("%.0f%% of cycle elapsed"), elapsed))
				})
			}
		})
		if timed || w.Reset != "" {
			ui.Row(c).FillWidth().Height(16).Gap(6).AlignItems(ui.Center).Children(func() {
				if timed {
					ui.Text(c, UsagePaceText(used, elapsed)).FontSize(10).TextColor(p.Muted).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").
						Tooltip(tr("Reference usage at a steady pace over the billing cycle, not the actual request rate."))
				}
				if w.Reset != "" {
					ui.Text(c, w.Reset+tr(" reset")).FontSize(10).TextColor(p.Muted).Shrink(0).SingleLine().Tooltip(w.Reset + tr(" reset"))
				}
			})
		}
	})
}

func UsagePaceText(used, elapsed float64) string {
	pace := int(math.Round(elapsed))
	delta := int(math.Round(elapsed - used))
	if delta > 0 {
		return fmt.Sprintf(tr("Steady pace %d%% · %d points under"), pace, delta)
	}
	if delta < 0 {
		return fmt.Sprintf(tr("Steady pace %d%% · %d points over"), pace, -delta)
	}
	return fmt.Sprintf(tr("Steady pace %d%% · On pace"), pace)
}

func usagePanelEmpty(c *ui.Context, r agentusage.Reading, busy bool) {
	title, message := tr("No usage data"), tr("Refresh to read local accounts.")
	if r.Error != "" {
		title, message = tr("Failed to load"), r.Error
	}
	if busy {
		title, message = tr("Loading"), tr("Checking ")+agentusage.AgentName(r.Agent)+tr(" account.")
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

func UsagePanelStatus(r agentusage.Reading, busy bool, now time.Time) string {
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
		return action + " just now"
	}
	format := "15:04"
	if y, m, d := r.At.Local().Date(); y != now.Year() || m != now.Month() || d != now.Day() {
		format = "01-02 15:04"
	}
	return r.At.Local().Format(format) + " " + action
}
