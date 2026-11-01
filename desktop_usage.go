package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"miya/internal/agentusage"
)

type usagePreferences struct {
	hidden, agentIcon bool
	agent             string
}

func loadUsagePreferences() usagePreferences {
	p := usagePreferences{hidden: loadSetting("usageTray") == "off", agentIcon: loadSetting("usageTrayIcon") == "agent", agent: loadSetting("usageTrayAgent")}
	if p.agent != "codex" && p.agent != "claude" {
		p.agent = ""
	}
	return p
}

type desktopUsage struct {
	readings                         []agentusage.Reading
	prefs                            usagePreferences
	busy, closed                     bool
	cancel                           context.CancelFunc
	timer                            *time.Timer
	themeOff                         func()
	tray                             *mygo.Tray
	trayKey                          string
	panel                            *mygo.Window
	panelAgent                       string
	panelHeight                      int
	resetOpen, resetting, resetRetry bool
	resetAttempt                     *agentusage.ResetAttempt
	resetMessage                     string
	resetReading                     agentusage.Reading
}

func (app *chatApp) ensureUsage() *desktopUsage {
	if app.usage == nil {
		app.usage = &desktopUsage{prefs: loadUsagePreferences()}
	}
	return app.usage
}

func (app *chatApp) startUsage() {
	d := app.ensureUsage()
	d.themeOff = mygo.Theme.OnUpdated(app.invalidateUsageViews)
	app.syncUsageTray()
	app.refreshUsage()
}

func (app *chatApp) stopUsage() {
	d := app.usage
	if d == nil || d.closed {
		return
	}
	d.closed = true
	app.closeUsagePanel()
	if d.cancel != nil {
		d.cancel()
	}
	if d.timer != nil {
		d.timer.Stop()
	}
	if d.themeOff != nil {
		d.themeOff()
	}
	if d.tray != nil {
		d.tray.Destroy()
		d.tray = nil
	}
}

func (app *chatApp) refreshUsage() {
	d := app.ensureUsage()
	if d.busy || d.closed {
		return
	}
	if d.timer != nil {
		d.timer.Stop()
	}
	d.busy = true
	app.invalidateUsageViews()
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	go func() {
		defer cancel()
		readings := make([]agentusage.Reading, 2)
		var wg sync.WaitGroup
		for i, agent := range []string{"codex", "claude"} {
			wg.Go(func() { readings[i] = agentusage.Read(ctx, agent) })
		}
		wg.Wait()
		// The tray also runs while the main window is hidden or closed.
		mygo.RunOnMain(func() {
			if d.closed {
				return
			}
			d.busy, d.cancel, d.readings = false, nil, readings
			app.syncUsageTray()
			app.invalidateUsageViews()
			d.timer = time.AfterFunc(3*time.Minute, func() { mygo.RunOnMain(app.refreshUsage) })
		})
	}()
}

func selectedUsage(readings []agentusage.Reading, agent string) *agentusage.Reading {
	for _, candidate := range []string{"codex", "claude"} {
		if agent != "" && agent != candidate {
			continue
		}
		for i := range readings {
			r := &readings[i]
			if r.Agent == candidate && r.Error == "" && len(r.Windows) > 0 {
				return r
			}
		}
	}
	return nil
}

func usageAgentName(agent string) string {
	if agent == "claude" {
		return "Claude"
	}
	return "Codex"
}

func remainingUsage(used float64) int { return int(math.Round(100 - agentusage.Percent(used))) }

func usageTrayTitle(r *agentusage.Reading) string {
	if r == nil || len(r.Windows) == 0 {
		return "—"
	}
	labels := make([]string, 0, 2)
	for _, w := range r.Windows[:min(2, len(r.Windows))] {
		labels = append(labels, fmt.Sprintf("%d%%", remainingUsage(w.Used)))
	}
	return strings.Join(labels, " · ")
}

func usageTrayTip(r *agentusage.Reading) string {
	if r == nil {
		return "Miya · 暂无额度数据\n点击查看"
	}
	lines := []string{"Miya · " + usageAgentName(r.Agent)}
	for _, w := range r.Windows {
		text := fmt.Sprintf("%s · 剩余 %d%%", w.Name, remainingUsage(w.Used))
		if w.Reset != "" {
			text += " · " + w.Reset + " 重置"
		}
		lines = append(lines, text)
	}
	if r.Resets != nil {
		lines = append(lines, fmt.Sprintf("%d 张重置卡", r.Resets.Count))
	}
	if !r.At.IsZero() {
		lines = append(lines, r.At.Local().Format("15:04")+" 更新")
	}
	return strings.Join(lines, "\n")
}

func (app *chatApp) syncUsageTray() {
	d := app.usage
	if d == nil || d.closed || !mygo.App.IsReady() {
		return
	}
	if d.prefs.hidden {
		app.closeUsagePanel()
		if d.tray != nil {
			d.tray.Destroy()
			d.tray = nil
		}
		d.trayKey = ""
		return
	}
	r := selectedUsage(d.readings, d.prefs.agent)
	agent := ""
	if d.prefs.agentIcon {
		agent = d.prefs.agent
		if agent == "" && r != nil {
			agent = r.Agent
		}
		if agent == "" {
			agent = "codex"
		}
	}
	tip, title := usageTrayTip(r), usageTrayTitle(r)
	key := agent + "\x00" + title + "\x00" + tip
	if d.tray != nil && d.trayKey == key {
		return
	}
	png := usageTrayIconFor(agent)
	if d.tray == nil {
		tray, err := mygo.NewTray(mygo.TrayOptions{ID: "miya.usage", Icon: png, IconIsTemplate: true, Title: title, ToolTip: tip})
		if err != nil {
			log.Printf("miya: tray: %v", err)
			return
		}
		d.tray = tray
		menu := mygo.NewMenu([]*mygo.MenuItem{
			{Label: "用量", Click: func(*mygo.MenuItem, *mygo.Window) { app.toggleUsagePanel() }},
			{Label: "打开 Miya", Click: func(*mygo.MenuItem, *mygo.Window) { app.closeUsagePanel(); app.showMainWindow() }},
			{Label: "用量设置", Click: func(*mygo.MenuItem, *mygo.Window) { app.openUsageSettingsFromPanel() }},
			mygo.Separator(),
			{Label: "退出 Miya", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
		})
		if runtime.GOOS == "linux" {
			tray.SetMenu(menu)
		} else {
			tray.OnClick(app.toggleUsagePanel)
			tray.OnRightClick(func() {
				app.closeUsagePanel()
				tray.SetMenu(menu)
				defer tray.SetMenu(nil)
				tray.PopUpMenu()
			})
		}
	} else {
		_ = d.tray.SetIcon(png, true)
		d.tray.SetTitle(title)
		d.tray.SetToolTip(tip)
	}
	d.trayKey = key
}

func (app *chatApp) showMainWindow() {
	if app.openWindow != nil {
		app.openWindow()
	}
	if w := currentWindow(); w != nil && !w.IsDestroyed() {
		w.Show()
		w.Focus()
		w.Invalidate()
		mygo.App.Focus()
	}
}

func (app *chatApp) openUsageSettingsFromPanel() {
	app.closeUsagePanel()
	app.section, app.settingsTab = "Settings", 3
	app.closeMenus()
	app.showMainWindow()
}
