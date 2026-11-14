package nsui

import (
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/agent/quota"
	"miya/internal/prefs"
)

func (d *desktopUsage) groups() []ui.SettingsGroup {
	agents := []ui.Option[string]{{Value: "", Label: tr("Automatic")}}
	for _, agent := range usageAgents() {
		agents = append(agents, ui.Option[string]{Value: agent, Label: quota.AgentName(agent)})
	}
	return []ui.SettingsGroup{
		{ID: "menu-bar", Title: tr("Menu bar"), Items: []ui.SettingsItem{
			{ID: "usage-tray", Title: tr("Menu bar usage"), Description: tr("Show subscription quota in the menu bar."), Control: settingsToggle("usage-tray", tr("Show menu bar usage"), func() bool { d.revision.Get(); return !d.hidden }, func(on bool) {
				d.hidden = !on
				v := "off"
				if on {
					v = "on"
				}
				prefs.Save("usageTray", v)
				d.changed()
			}, nil)},
			{ID: "usage-agent", Title: tr("Account to show"), Control: ui.Select[string]{TestID: "usage-agent", Label: tr("Tray account"), Options: agents, Value: func() string { d.revision.Get(); return d.agent }, OnChange: func(v string) { d.agent = v; prefs.Save("usageTrayAgent", v); d.changed() }}},
			{ID: "usage-icon", Title: tr("Icon"), Control: ui.Select[string]{TestID: "usage-icon", Label: tr("Tray icon"), Options: []ui.Option[string]{{Value: "miya", Label: "Miya"}, {Value: "agent", Label: tr("Agent")}}, Value: func() string {
				d.revision.Get()
				if d.agentIcon {
					return "agent"
				}
				return "miya"
			}, OnChange: func(v string) { d.agentIcon = v == "agent"; prefs.Save("usageTrayIcon", v); d.changed() }}},
		}},
		{ID: "usage-subscriptions", Title: tr("Usage"), Actions: []core.Node{settingsButton("usage-settings-refresh", tr("Refresh subscription quota"), func() { d.c.RefreshAccount(); d.refresh() }, func() bool { d.revision.Get(); return d.busy })}, Items: []ui.SettingsItem{{ID: "usage-readings", Content: core.Func(func(cx *core.Scope) core.Node {
			title, help := tr("Local subscriptions"), tr("Refreshes every 3 minutes using the Codex and Claude Code accounts signed in on this computer.")
			if d.c.RemoteHost() {
				title, help = tr("Server subscriptions"), tr("Refreshes every 3 minutes using the Codex and Claude Code accounts signed in on the server.")
			}
			nodes := []core.Node{settingsText(cx, func() string { return title + "\n" + help })}
			for _, agent := range usageAgents() {
				nodes = append(nodes, d.subscriptionCard(cx, agent))
			}
			return settingsColumn(nodes...)
		})}}},
	}
}
