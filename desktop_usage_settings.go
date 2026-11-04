package main

import (
	"github.com/egoist/mygo/ui"
)

func (app *chatApp) usageSettings(c *ui.Context) {
	d := app.ensureUsage()
	ui.Column(c).FillWidth().Gap(20).Children(func() {
		title, help := tr("Local subscriptions"), tr("Refreshes every 3 minutes using the Codex and Claude Code accounts signed in on this computer.")
		if app.c.remoteHost() {
			title, help = tr("Server subscriptions"), tr("Refreshes every 3 minutes using the Codex and Claude Code accounts signed in on the server.")
		}
		settingsHeader(c, title, help, func() {
			if ui.Button(c, tr("Refresh")).Label(tr("Refresh subscription quota")).Disabled(d.busy).Clicked() {
				app.refreshUsage()
			}
		})
		settingsGroup(c, tr("Menu bar"), "",
			func() {
				settingRow(c, tr("Menu bar usage"), tr("Show subscription quota in the menu bar."), func() {
					show := !d.prefs.hidden
					if ui.Switch(c, &show).Label(tr("Show menu bar usage")).Changed() {
						d.prefs.hidden = !show
						value := "on"
						if !show {
							value = "off"
						}
						persistSetting("usageTray", value)
						app.syncUsageTray()
					}
				})
			},
			func() {
				settingRow(c, tr("Account to show"), "", func() {
					agents, index := []string{"", "codex", "claude"}, 0
					for i, agent := range agents {
						if d.prefs.agent == agent {
							index = i
						}
					}
					if ui.Segmented(c, &index, tr("Automatic"), "Codex", "Claude").Label(tr("Tray account")).Changed() {
						d.prefs.agent = agents[index]
						persistSetting("usageTrayAgent", d.prefs.agent)
						app.syncUsageTray()
					}
				})
			},
			func() {
				settingRow(c, tr("Icon"), "", func() {
					index := 0
					if d.prefs.agentIcon {
						index = 1
					}
					if ui.Segmented(c, &index, "Miya", tr("Agent")).Label(tr("Tray icon")).Changed() {
						d.prefs.agentIcon = index == 1
						value := "miya"
						if d.prefs.agentIcon {
							value = "agent"
						}
						persistSetting("usageTrayIcon", value)
						app.syncUsageTray()
					}
				})
			})
		_, palette := app.usageTheme(c)
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			ui.Text(c, tr("Account")).FontSize(12).FontWeight(600).TextColor(muted).PaddingX(4)
			ui.Column(c).FillWidth().Gap(12).Children(func() {
				for _, agent := range []string{"codex", "claude"} {
					r := d.reading(agent)
					settingsCard(c.Key(agent)).Gap(14).Children(func() {
						ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
							name := agentCodex
							if agent == "claude" {
								name = agentClaude
							}
							agentIcon(c, name, 18)
							ui.Text(c, usageAgentName(agent)).Grow(1).FontSize(14).FontWeight(600)
							if r.Plan != "" {
								badge(c, r.Plan)
							}
						})
						if agent == "codex" && d.resetOpen {
							app.usageResetConfirmation(c, r)
							return
						}
						if len(r.Windows) == 0 && r.Resets == nil {
							usagePanelEmpty(c, r, d.busy)
						}
						for _, w := range r.Windows {
							usagePanelWindow(c, w, palette)
						}
						if agent == "codex" {
							app.usageResetSection(c, r, false)
						}
						ui.Text(c, usagePanelStatus(r, d.busy, c.Now())).FontSize(11).TextColor(muted)
					})
				}
			})
		})
	})
}
