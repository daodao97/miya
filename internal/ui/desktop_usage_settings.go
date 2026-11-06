package desktopui

import (
	appmodel "miya/internal/appmodel"
	widgets "miya/internal/widgets"

	ui "github.com/egoist/mygo/ui"
)

func (app *App) usageSettings(c *ui.Context) {
	d := app.EnsureUsage()
	ui.Column(c).FillWidth().Gap(20).Children(func() {
		title, help := tr("Local subscriptions"), tr("Refreshes every 3 minutes using the Codex and Claude Code accounts signed in on this computer.")
		if app.C.RemoteHost() {
			title, help = tr("Server subscriptions"), tr("Refreshes every 3 minutes using the Codex and Claude Code accounts signed in on the server.")
		}
		widgets.SettingsHeader(c, title, help, func() {
			if ui.Button(c, tr("Refresh")).Label(tr("Refresh subscription quota")).Disabled(d.Busy).Clicked() {
				app.refreshUsage()
			}
		})
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
						appmodel.PersistSetting("usageTray", value)
						app.syncUsageTray()
					}
				})
			},
			func() {
				widgets.SettingRow(c, tr("Account to show"), "", func() {
					agents, index := []string{"", "codex", "claude"}, 0
					for i, agent := range agents {
						if d.Prefs.Agent == agent {
							index = i
						}
					}
					if ui.Segmented(c, &index, tr("Automatic"), "Codex", "Claude").Label(tr("Tray account")).Changed() {
						d.Prefs.Agent = agents[index]
						appmodel.PersistSetting("usageTrayAgent", d.Prefs.Agent)
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
						appmodel.PersistSetting("usageTrayIcon", value)
						app.syncUsageTray()
					}
				})
			})
		_, palette := app.usageTheme(c)
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			ui.Text(c, tr("Account")).FontSize(12).FontWeight(600).TextColor(widgets.Muted).PaddingX(4)
			ui.Column(c).FillWidth().Gap(12).Children(func() {
				for _, agent := range []string{"codex", "claude"} {
					r := d.reading(agent)
					widgets.SettingsCard(c.Key(agent)).Gap(14).Children(func() {
						ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
							name := appmodel.AgentCodex
							if agent == "claude" {
								name = appmodel.AgentClaude
							}
							widgets.AgentIcon(c, name, 18)
							ui.Text(c, appmodel.UsageAgentName(agent)).Grow(1).FontSize(14).FontWeight(600)
							if r.Plan != "" {
								badge(c, r.Plan)
							}
						})
						if agent == "codex" && d.ResetOpen {
							app.usageResetConfirmation(c, r)
							return
						}
						if len(r.Windows) == 0 && r.Resets == nil {
							usagePanelEmpty(c, r, d.Busy)
						}
						for _, w := range r.Windows {
							usagePanelWindow(c, w, palette)
						}
						if agent == "codex" {
							app.usageResetSection(c, r, false)
						}
						ui.Text(c, UsagePanelStatus(r, d.Busy, c.Now())).FontSize(11).TextColor(widgets.Muted)
					})
				}
			})
		})
	})
}
