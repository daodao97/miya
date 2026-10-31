package main

import "github.com/egoist/mygo/ui"

func notificationSettings(c *ui.Context) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "通知").FontWeight(600)
		for _, option := range []struct{ key, label string }{{"completion", "任务完成提醒"}, {"waiting", "等待确认提醒"}} {
			enabled := loadSetting("notifications-"+option.key) != "off"
			label := option.label + "：开启"
			if !enabled {
				label = option.label + "：关闭"
			}
			if quiet(c.Key(option.key)).Height(32).Children(func() { ui.Text(c, label) }).Clicked() {
				value := "on"
				if enabled {
					value = "off"
				}
				persistSetting("notifications-"+option.key, value)
			}
		}
		ui.Text(c, "手机后台通知需要在发送端配置 Miya 的 APNs 密钥，并在手机允许通知。").FontSize(12).TextColor(muted)
	})
}
