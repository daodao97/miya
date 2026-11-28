package nsui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"

	agentmeta "miya/internal/agent/catalog"
	"miya/internal/agent/quota"
	"miya/internal/prefs"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
)

var usageKey = core.NewKey[*desktopUsage]("miya-usage", nil)

// desktopUsage belongs to the active workspace's app scope, so hiding or
// closing the main window does not stop its polling. All fields are engine-owned.
type desktopUsage struct {
	cx                               *core.Scope
	c                                Host
	revision                         *core.Signal[int]
	category                         *core.Signal[string]
	readings                         []quota.Reading
	hidden, agentIcon                bool
	agent, panelAgent                string
	busy, active                     bool
	generation                       uint64
	cancel                           context.CancelFunc
	timer                            func() bool
	tray                             *desktop.Tray
	panel                            *desktop.Window
	trayKey                          string
	client                           *svcclient.Client
	remote                           bool
	openSettings                     func(string)
	reset                            *quota.ResetAttempt
	resetReading                     quota.Reading
	resetOpen, resetting, resetRetry bool
	resetMessage                     string
}

func usageFor(cx *core.Scope, c Host) *desktopUsage {
	if d := usageKey.Use(cx); d != nil {
		return d
	}
	d := newDesktopUsage(cx, c)
	usageKey.Provide(cx, d)
	d.start()
	return d
}

func newDesktopUsage(cx *core.Scope, c Host) *desktopUsage {
	agent := prefs.Load("usageTrayAgent")
	if !supportedUsageAgent(agent) {
		agent = ""
	}
	d := &desktopUsage{cx: cx, c: c, revision: core.NewSignal(cx, 0), category: core.NewSignal(cx, "general"), hidden: prefs.Load("usageTray") == "off", agentIcon: prefs.Load("usageTrayIcon") == "agent", agent: agent}
	cx.Cleanup(d.stop)
	cx.Effect(func() { Live(func() string { return prefs.Load("interfaceLanguage") })(); cx.Untrack(d.syncTray) })
	// Reconnects and changes of host must invalidate in-flight responses and
	// confirmations, even if the workspace itself has stayed selected.
	cx.Effect(func() {
		cl := Live(func() *svcclient.Client { return c.State().Remote })()
		remote := Live(c.RemoteHost)()
		cx.Untrack(func() {
			if d.client == cl && d.remote == remote {
				return
			}
			running := d.active
			d.stop()
			sameHost := d.remote == remote
			d.client, d.remote = cl, remote
			d.readings = nil
			if !sameHost {
				d.reset, d.resetMessage, d.resetRetry = nil, "", false
			}
			d.resetOpen = false
			if running {
				d.start()
			}
		})
	})
	return d
}
func (d *desktopUsage) changed() { d.revision.Update(func(v int) int { return v + 1 }); d.syncTray() }
func (d *desktopUsage) start() {
	if d.active {
		return
	}
	d.active = true
	// Menu-bar preferences are device-wide; another workspace may have
	// changed them since this one was last active.
	d.hidden, d.agentIcon = prefs.Load("usageTray") == "off", prefs.Load("usageTrayIcon") == "agent"
	d.agent = prefs.Load("usageTrayAgent")
	if !supportedUsageAgent(d.agent) {
		d.agent = ""
	}
	d.client, d.remote = d.c.State().Remote, d.c.RemoteHost()
	d.syncTray()
	d.refresh()
}
func (d *desktopUsage) stop() {
	d.active = false
	d.generation++
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	if d.timer != nil {
		d.timer()
		d.timer = nil
	}
	// A reset interrupted by switching workspace may already have redeemed a
	// card. Keep its operation key for an explicit retry on return.
	if d.resetting && d.reset != nil {
		d.resetRetry = true
		d.resetMessage = tr("The last result is unconfirmed. Retrying the same operation will not use another card.")
	}
	d.busy, d.resetting, d.resetOpen = false, false, false
	d.tray.Remove()
	d.tray = nil
	d.panel.Close()
	d.panel = nil
	d.trayKey = ""
}
func quotaService() *quota.Service {
	if environment.Quotas != nil {
		return environment.Quotas()
	}
	return quota.NewService()
}
func (d *desktopUsage) schedule() {
	d.cx.Run(func() { d.timer = d.cx.AfterFunc(3*time.Minute, d.refresh) })
}
func (d *desktopUsage) refresh() {
	if !d.active || d.busy {
		return
	}
	if d.timer != nil {
		d.timer()
		d.timer = nil
	}
	d.busy = true
	d.changed()
	service := quotaService()
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	gen, cl := d.generation, d.client
	// Local service mirrors should use the local integration, just as MyGo does.
	if !d.remote {
		cl = nil
	}
	remote := d.remote
	go func() {
		defer cancel()
		providers := agentmeta.Usage()
		readings := make([]quota.Reading, len(providers))
		if remote {
			var err error
			if cl == nil {
				err = svcproto.ErrClosed
			} else {
				err = cl.Call("host", svcproto.HostRequest{Action: "usage"}, &readings)
			}
			if err != nil {
				for i, p := range providers {
					readings[i] = quota.Reading{Agent: p.UsageID, Error: err.Error(), At: time.Now()}
				}
			}
		} else {
			var wg sync.WaitGroup
			for i, p := range providers {
				wg.Go(func() { readings[i] = service.Read(ctx, p.UsageID) })
			}
			wg.Wait()
		}
		d.cx.Post(func() {
			if !d.active || gen != d.generation {
				return
			}
			d.busy, d.cancel, d.readings = false, nil, readings
			d.changed()
			d.schedule()
		})
	}()
}
func usageAgents() []string {
	var ids []string
	for _, p := range agentmeta.Usage() {
		ids = append(ids, p.UsageID)
	}
	return ids
}
func supportedUsageAgent(id string) bool {
	for _, candidate := range usageAgents() {
		if candidate == id {
			return true
		}
	}
	return false
}
func (d *desktopUsage) reading(agent string) quota.Reading {
	d.revision.Get()
	for _, r := range d.readings {
		if r.Agent == agent {
			return r
		}
	}
	return quota.Reading{Agent: agent}
}
func usageTrayTip(r *quota.Reading) string {
	if r == nil {
		return tr("Miya · No usage data\nClick to view")
	}
	lines := []string{"Miya · " + quota.AgentName(r.Agent)}
	for _, w := range r.Windows {
		text := fmt.Sprintf(tr("%s · %d%% remaining"), w.Name, quota.RemainingUsage(w.Used))
		if w.Reset != "" {
			text += " · " + w.Reset + tr(" reset")
		}
		lines = append(lines, text)
	}
	if r.Resets != nil {
		lines = append(lines, fmt.Sprintf(tr("%d reset cards"), r.Resets.AvailableCount))
	}
	if !r.At.IsZero() {
		lines = append(lines, r.At.Local().Format("15:04")+tr(" updated"))
	}
	return strings.Join(lines, "\n")
}
func (d *desktopUsage) syncTray() {
	if !d.active {
		return
	}
	// Native surfaces must belong to the app scope, not whichever binding
	// happened to update the data or preferences.
	d.cx.Run(func() {
		if d.hidden {
			d.tray.Remove()
			d.tray = nil
			d.panel.Close()
			d.panel = nil
			d.trayKey = ""
			return
		}
		r := quota.SelectedUsage(d.readings, d.agent)
		agent := ""
		if d.agentIcon {
			agent = d.agent
			if agent == "" && r != nil {
				agent = r.Agent
			}
			if agent == "" && len(usageAgents()) > 0 {
				agent = usageAgents()[0]
			}
		}
		title, tip := quota.TrayTitle(r), usageTrayTip(r)
		key := agent + "\x00" + title + "\x00" + tip
		if d.tray != nil && d.trayKey == key {
			return
		}
		if d.panelAgent == "" {
			d.panelAgent = d.agent
			if r != nil {
				d.panelAgent = r.Agent
			}
			if d.panelAgent == "" && len(usageAgents()) > 0 {
				d.panelAgent = usageAgents()[0]
			}
		}
		if d.tray == nil {
			d.panel = desktop.OpenPanel(d.cx, desktop.PanelOptions{Width: usagePanelWidth, Height: usagePanelLimit,
				FitContent: true, HideOnBlur: true, Material: desktop.WindowMaterialPopover}, d.panelView)
			d.tray = desktop.NewTray(d.cx, desktop.TrayOptions{Icon: UsageTrayIconFor(agent), Template: true, Title: title, Tooltip: tip, Panel: d.panel, Menu: d.menu})
		} else {
			d.tray.SetIcon(UsageTrayIconFor(agent), true)
			d.tray.SetTitle(title)
			d.tray.SetTooltip(tip)
		}
		d.trayKey = key
	})
}
func (d *desktopUsage) showSettings(category string) {
	d.panel.Hide()
	desktop.MainWindow(d.cx).Focus()
	if d.openSettings != nil {
		d.openSettings(category)
	}
}
func (d *desktopUsage) menu() []core.MenuItem {
	return []core.MenuItem{
		{Label: tr("Usage"), OnSelect: func() {
			if d.panel != nil {
				d.panel.Toggle()
			} else {
				d.showSettings("usage")
			}
		}},
		{Label: tr("Open Miya"), OnSelect: func() { d.panel.Hide(); desktop.MainWindow(d.cx).Focus() }},
		{Label: tr("Account"), OnSelect: func() { d.showSettings("account") }},
		{Separator: true}, {Label: tr("Quit Miya"), OnSelect: d.cx.Quit},
	}
}
