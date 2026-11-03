package main

import (
	"github.com/egoist/mygo/ui"
)

func (app *chatApp) usageSettings(c *ui.Context) {
	d := app.ensureUsage()
	ui.Column(c).FillWidth().Gap(20).Children(func() {
		settingsHeader(c, "本机订阅", "每 3 分钟刷新。读取本机 Codex 和 Claude Code 当前登录的账号。", func() {
			if ui.Button(c, "刷新").Label("刷新订阅额度").Disabled(d.busy).Clicked() {
				app.refreshUsage()
			}
		})
		settingsGroup(c, "菜单栏", "",
			func() {
				settingRow(c, "菜单栏用量", "在菜单栏显示订阅额度。", func() {
					show := !d.prefs.hidden
					if ui.Switch(c, &show).Label("显示菜单栏用量").Changed() {
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
				settingRow(c, "显示账号", "", func() {
					agents, index := []string{"", "codex", "claude"}, 0
					for i, agent := range agents {
						if d.prefs.agent == agent {
							index = i
						}
					}
					if ui.Segmented(c, &index, "自动", "Codex", "Claude").Label("托盘账号").Changed() {
						d.prefs.agent = agents[index]
						persistSetting("usageTrayAgent", d.prefs.agent)
						app.syncUsageTray()
					}
				})
			},
			func() {
				settingRow(c, "图标", "", func() {
					index := 0
					if d.prefs.agentIcon {
						index = 1
					}
					if ui.Segmented(c, &index, "Miya", "Agent").Label("托盘图标").Changed() {
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
			ui.Text(c, "账号").FontSize(12).FontWeight(600).TextColor(muted).PaddingX(4)
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
