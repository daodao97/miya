package desktopui

import (
	"github.com/egoist/mygo/ui"

	agentmeta "miya/internal/agent/catalog"
	agentusage "miya/internal/agent/quota"
	"miya/internal/prefs"
	"miya/internal/widgets"
)

func (s *accountView) subscriptionSettings(c *ui.Context) {
	app, cc := s.app, s.app.C
	d := app.EnsureUsage()
	data, action := cc.State().AccountState, cc.State().AccountReset
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		title, help := tr("Local subscriptions"), tr("Refreshes every 3 minutes using the Codex and Claude Code accounts signed in on this computer.")
		if cc.RemoteHost() {
			title, help = tr("Server subscriptions"), tr("Refreshes every 3 minutes using the Codex and Claude Code accounts signed in on the server.")
		}
		widgets.SettingsHeader(c, title, help, func() {
			if ui.Button(c, tr("Refresh")).Label(tr("Refresh subscription quota")).Disabled(d.Busy || data.Loading || data.Refreshing || action.Busy).Clicked() {
				cc.RefreshAccount()
				app.refreshUsage()
			}
		})
		for _, agent := range usageAgents() {
			if agent == "codex" {
				r := d.reading(agent)
				// Keep the richer connected-account view (including extra credits).
				// The standalone reading remains available without a chat connection.
				if data.Identity != nil || len(r.Windows) == 0 && r.Resets == nil {
					ui.Text(c, tr("Codex account")).FontSize(14).FontWeight(600)
					AccountDetails(c.Key("codex-details"), data)
					if data.Identity != nil && data.Identity.Type == "chatgpt" {
						ResetCreditCard(c.Key("codex-reset"), data, action, cc.State().Ready(), cc.AskAccountReset, cc.CancelAccountReset, cc.ConfirmAccountReset)
					}
					continue
				}
			}
			app.subscriptionUsageCard(c.Key(agent), agent)
		}
	})
}

func (app *App) usageMenuBarSettings(c *ui.Context) {
	d := app.EnsureUsage()
	ui.Column(c).FillWidth().Gap(20).Children(func() {
		widgets.SettingsGroup(c, tr("Menu bar"), "",
			func() {
				widgets.SettingRow(c, tr("Menu bar usage"), tr("Show subscription quota in the menu bar."), func() {
					show := !d.Prefs.Hidden
					if ui.Switch(c, &show).Label(tr("Show menu bar usage")).Changed() {
						d.Prefs.Hidden = !show
						value := "on"
						if !show {
							value = "off"
						}
						prefs.Save("usageTray", value)
						app.syncUsageTray()
					}
				})
			},
			func() {
				widgets.SettingRow(c, tr("Account to show"), "", func() {
					agents, index := append([]string{""}, usageAgents()...), 0
					labels := []string{tr("Automatic")}
					for _, id := range agents[1:] {
						labels = append(labels, agentusage.AgentName(id))
					}
					for i, agent := range agents {
						if d.Prefs.Agent == agent {
							index = i
						}
					}
					if ui.Segmented(c, &index, labels...).Label(tr("Tray account")).Changed() {
						d.Prefs.Agent = agents[index]
						prefs.Save("usageTrayAgent", d.Prefs.Agent)
						app.syncUsageTray()
					}
				})
			},
			func() {
				widgets.SettingRow(c, tr("Icon"), "", func() {
					index := 0
					if d.Prefs.AgentIcon {
						index = 1
					}
					if ui.Segmented(c, &index, "Miya", tr("Agent")).Label(tr("Tray icon")).Changed() {
						d.Prefs.AgentIcon = index == 1
						value := "miya"
						if d.Prefs.AgentIcon {
							value = "agent"
						}
						prefs.Save("usageTrayIcon", value)
						app.syncUsageTray()
					}
				})
			})
	})
}

func (app *App) subscriptionUsageCard(c *ui.Context, agent string) {
	d := app.EnsureUsage()
	r := d.reading(agent)
	_, palette := app.usageTheme(c)
	widgets.SettingsCard(c).Gap(14).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			widgets.AgentIcon(c, agentmeta.UsageFor(agent).ID, 18)
			ui.Text(c, agentusage.AgentName(agent)).Grow(1).FontSize(14).FontWeight(600)
			if r.Plan != "" {
				badge(c, r.Plan)
			}
		})
		if agentmeta.UsageFor(agent).UsageReset && d.ResetOpen {
			app.usageResetConfirmation(c, r)
			return
		}
		if len(r.Windows) == 0 && r.Resets == nil {
			usagePanelEmpty(c, r, d.Busy)
		}
		for _, w := range r.Windows {
			usagePanelWindow(c, w, palette)
		}
		if agentmeta.UsageFor(agent).UsageReset {
			app.usageResetSection(c, r, false)
		}
		ui.Text(c, UsagePanelStatus(r, d.Busy, c.Now())).FontSize(11).TextColor(widgets.Muted)
	})
}
