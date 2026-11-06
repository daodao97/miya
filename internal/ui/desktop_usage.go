package desktopui

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"miya/internal/agentmeta"
	"miya/internal/agentusage"
	"miya/internal/prefs"
	"miya/internal/svcproto"
	"miya/internal/uiruntime"
)

type UsagePreferences struct {
	Hidden, AgentIcon bool
	Agent             string
}

func LoadUsagePreferences() UsagePreferences {
	p := UsagePreferences{Hidden: prefs.Load("usageTray") == "off", AgentIcon: prefs.Load("usageTrayIcon") == "agent", Agent: prefs.Load("usageTrayAgent")}
	if !supportedUsageAgent(p.Agent) {
		p.Agent = ""
	}
	return p
}

type DesktopUsage struct {
	Readings                         []agentusage.Reading
	Prefs                            UsagePreferences
	Busy, closed                     bool
	cancel                           context.CancelFunc
	timer                            *time.Timer
	themeOff                         func()
	tray                             *mygo.Tray
	trayKey                          string
	panel                            *mygo.Window
	PanelAgent                       string
	panelHeight                      int
	ResetOpen, Resetting, resetRetry bool
	ResetAttempt                     *agentusage.ResetAttempt
	resetMessage                     string
	resetReading                     agentusage.Reading
}

func (app *App) EnsureUsage() *DesktopUsage {
	if app.Usage == nil {
		app.Usage = &DesktopUsage{Prefs: LoadUsagePreferences()}
	}
	return app.Usage
}

func (app *App) startUsage() {
	d := app.EnsureUsage()
	d.themeOff = mygo.Theme.OnUpdated(app.invalidateUsageViews)
	app.syncUsageTray()
	app.refreshUsage()
}

func (app *App) stopUsage() {
	d := app.Usage
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

func (app *App) refreshUsage() {
	d := app.EnsureUsage()
	if d.Busy || d.closed {
		return
	}
	if d.timer != nil {
		d.timer.Stop()
	}
	d.Busy = true
	app.invalidateUsageViews()
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	var cl *svcproto.Client
	if app.C.RemoteHost() {
		cl = app.C.State().Remote
	}
	go func() {
		defer cancel()
		providers := agentmeta.Usage()
		readings := make([]agentusage.Reading, len(providers))
		if cl != nil {
			if err := cl.Call("host", svcproto.HostRequest{Action: "usage"}, &readings); err != nil {
				for i, p := range providers {
					readings[i] = agentusage.Reading{Agent: p.UsageID, Error: err.Error()}
				}
			}
		} else {
			var wg sync.WaitGroup
			for i, provider := range providers {
				agent := provider.UsageID
				wg.Go(func() { readings[i] = agentusage.Read(ctx, agent) })
			}
			wg.Wait()
		}
		// The tray also runs while the main window is hidden or closed.
		mygo.RunOnMain(func() {
			if d.closed {
				return
			}
			d.Busy, d.cancel, d.Readings = false, nil, readings
			app.syncUsageTray()
			app.invalidateUsageViews()
			d.timer = time.AfterFunc(3*time.Minute, func() { mygo.RunOnMain(app.refreshUsage) })
		})
	}()
}

func usageTrayTip(r *agentusage.Reading) string {
	if r == nil {
		return tr("Miya · No usage data\nClick to view")
	}
	lines := []string{"Miya · " + agentusage.AgentName(r.Agent)}
	for _, w := range r.Windows {
		text := fmt.Sprintf(tr("%s · %d%% remaining"), w.Name, agentusage.RemainingUsage(w.Used))
		if w.Reset != "" {
			text += " · " + w.Reset + tr(" reset")
		}
		lines = append(lines, text)
	}
	if r.Resets != nil {
		lines = append(lines, fmt.Sprintf(tr("%d reset cards"), r.Resets.Count))
	}
	if !r.At.IsZero() {
		lines = append(lines, r.At.Local().Format("15:04")+tr(" updated"))
	}
	return strings.Join(lines, "\n")
}

func (app *App) syncUsageTray() {
	d := app.Usage
	if d == nil || d.closed || !mygo.App.IsReady() {
		return
	}
	if d.Prefs.Hidden {
		app.closeUsagePanel()
		if d.tray != nil {
			d.tray.Destroy()
			d.tray = nil
		}
		d.trayKey = ""
		return
	}
	r := agentusage.SelectedUsage(d.Readings, d.Prefs.Agent)
	agent := ""
	if d.Prefs.AgentIcon {
		agent = d.Prefs.Agent
		if agent == "" && r != nil {
			agent = r.Agent
		}
		if agent == "" {
			agent = agentmeta.Usage()[0].UsageID
		}
	}
	tip, title := usageTrayTip(r), agentusage.TrayTitle(r)
	key := agent + "\x00" + title + "\x00" + tip + "\x00" + tr("Usage")
	if d.tray != nil && d.trayKey == key {
		return
	}
	png := UsageTrayIconFor(agent)
	if d.tray == nil {
		tray, err := mygo.NewTray(mygo.TrayOptions{ID: "miya.usage", Icon: png, IconIsTemplate: true, Title: title, ToolTip: tip})
		if err != nil {
			log.Printf("miya: tray: %v", err)
			return
		}
		d.tray = tray

		if runtime.GOOS == "linux" {
			tray.SetMenu(app.usageTrayMenu())
		} else {
			tray.OnClick(app.toggleUsagePanel)
			tray.OnRightClick(func() {
				menu := app.usageTrayMenu()
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
		if runtime.GOOS == "linux" {
			d.tray.SetMenu(app.usageTrayMenu())
		}
	}
	d.trayKey = key
}

func (app *App) showMainWindow() {
	if app.openWindow != nil {
		app.openWindow()
	}
	if w := uiruntime.CurrentWindow(); w != nil && !w.IsDestroyed() {
		w.Show()
		w.Focus()
		w.Invalidate()
		mygo.App.Focus()
	}
}

func (app *App) openUsageSettingsFromPanel() {
	app.closeUsagePanel()
	app.Section, app.SettingsTab = "Settings", 3
	app.closeMenus()
	app.showMainWindow()
}

func (app *App) usageTrayMenu() *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: tr("Usage"), Click: func(*mygo.MenuItem, *mygo.Window) { app.toggleUsagePanel() }},
		{Label: tr("Open Miya"), Click: func(*mygo.MenuItem, *mygo.Window) { app.closeUsagePanel(); app.showMainWindow() }},
		{Label: tr("Usage settings"), Click: func(*mygo.MenuItem, *mygo.Window) { app.openUsageSettingsFromPanel() }},
		mygo.Separator(),
		{Label: tr("Quit Miya"), Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	})
}

func usageAgents() []string {
	var out []string
	for _, d := range agentmeta.Usage() {
		out = append(out, d.UsageID)
	}
	return out
}
func supportedUsageAgent(id string) bool {
	for _, candidate := range usageAgents() {
		if candidate == id {
			return true
		}
	}
	return false
}
