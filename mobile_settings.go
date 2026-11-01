package main

import (
	"fmt"
	"github.com/egoist/mygo/ui"
)

func (m *mobileApp) settingsView(c *ui.Context) {
	p := m.pal
	ui.Row(c).FillWidth().MinHeight(64).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		m.navigationBack(c, "/settings")
		ui.Text(c, "设置").Grow(1).TextAlign(ui.Center).FontSize(m.font(16)).FontWeight(600)
		ui.Box(c.Key("balance")).Size(44, 44)
	})
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(24, 16).Gap(12).Children(func() {
			ui.Text(c, "桌面端连接").FontSize(m.font(13)).TextColor(p.muted).Margin(0, 12)
			ui.Text(c, firstNonEmpty(m.host, "桌面端")).FontSize(m.font(17)).FontWeight(500).Margin(0, 12)
			if status := m.connectionLine(); status != "" {
				ui.Text(c, status).FontSize(m.font(13)).TextColor(p.muted).Margin(0, 12)
			}
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				if m.pill(c, "reconnect", "重新连接", true).Grow(1).Clicked() {
					m.connect(m.link)
				}
				if m.pill(c, "manage-desktops", "管理桌面", false).Grow(1).Clicked() {
					m.openDesktopManager()
				}
			})
			if m.pill(c, "add-desktop", "添加桌面", false).FillWidth().Clicked() {
				m.addDesktop()
			}
			ui.Text(c, "对话").FontSize(m.font(13)).TextColor(p.muted).Margin(0, 12)
			if m.pill(c, "message-options", "模型与权限", false).FillWidth().MinHeight(48).Clicked() {
				m.openOptions(m.composerAgent())
			}
			ui.Text(c, "通知").FontSize(m.font(13)).TextColor(p.muted).Margin(0, 12)
			ui.Row(c).FillWidth().Padding(18, 16).Gap(16).AlignItems(ui.Center).Background(p.surface).Radius(18).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
					ui.Text(c, "手机通知").FontSize(m.font(16)).FontWeight(500)
					ui.Text(c, "接收任务完成和等待确认提醒").FontSize(m.font(12)).TextColor(p.muted)
				})
				enabled := !m.pushDisabled
				if ui.Switch(c.Key("push-toggle"), &enabled).Label("开启手机通知").Shrink(0).Changed() {
					m.setPushEnabled(enabled)
				}
			})
			ui.Text(c, "请同时在系统设置中允许 Miya 发送通知。").FontSize(m.font(12)).TextColor(p.muted).Margin(0, 12)
			ui.Row(c).FillWidth().Padding(14, 12).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf("未读对话 %d", len(m.state.Notifications.Unread))).Grow(1).FontSize(m.font(14))
				if m.pill(c, "read-all", "全部标为已读", false).Disabled(len(m.state.Notifications.Unread) == 0 || !m.connected).Clicked() {
					m.markAllRead()
				}
			})
			if !m.pushDisabled && m.pushError != "" {
				ui.Text(c, m.pushError).FontSize(m.font(13)).TextColor(danger).Margin(0, 12)
			}
		})
	})
}

func (m *mobileApp) setPushEnabled(enabled bool) {
	m.pushDisabled = !enabled
	m.updateBadge()
	value := "off"
	if enabled {
		value = "on"
	}
	persistSetting("phone-notifications", value)
	m.syncPush()
	if enabled {
		m.requestPush()
	}
}
