package main

import (
	"github.com/egoist/mygo/ui"
)

func (app *chatApp) usageSettings(c *ui.Context) {
	d := app.ensureUsage()
	ui.Column(c).Gap(20).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, "本机订阅").FontSize(16).FontWeight(600)
				ui.Text(c, "每 3 分钟刷新。账户与本机 CLI 一致。").FontSize(12).TextColor(muted)
			})
			if ui.Button(c, "刷新").Label("刷新订阅额度").Disabled(d.busy).Clicked() {
				app.refreshUsage()
			}
		})
		card(c).Padding(16).Gap(16).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, "菜单栏用量").Grow(1)
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
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "显示账户").Grow(1)
				agents, index := []string{"", "codex", "claude"}, 0
				for i, agent := range agents {
					if d.prefs.agent == agent {
						index = i
					}
				}
				if ui.Segmented(c, &index, "自动", "Codex", "Claude").Label("托盘账户").Changed() {
					d.prefs.agent = agents[index]
					persistSetting("usageTrayAgent", d.prefs.agent)
					app.syncUsageTray()
				}
			})
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "图标").Grow(1)
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
		for _, agent := range []string{"codex", "claude"} {
			r := d.reading(agent)
			card(c.Key(agent)).Padding(18).Gap(18).Children(func() {
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
}
