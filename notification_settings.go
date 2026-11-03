package main

import "github.com/egoist/mygo/ui"

func notificationSettings(c *ui.Context, zh bool) {
	options := []struct{ key, zh, en, zhHelp, enHelp string }{
		{"completion", "任务完成提醒", "Task finished", "对话完成一轮任务时发送通知。", "Notify when a conversation finishes its turn."},
		{"waiting", "等待确认提醒", "Waiting for approval", "需要你批准操作时发送通知。", "Notify when an action needs your approval."},
	}
	rows := make([]func(), 0, len(options))
	for _, option := range options {
		option := option
		rows = append(rows, func() {
			label := zhOr(zh, option.zh, option.en)
			enabled := loadSetting("notifications-"+option.key) != "off"
			settingRow(c, label, zhOr(zh, option.zhHelp, option.enHelp), func() {
				on := enabled
				ui.Switch(c.Key(option.key), &on).Label(label)
				if on != enabled {
					value := "off"
					if on {
						value = "on"
					}
					persistSetting("notifications-"+option.key, value)
				}
			})
		})
	}
	settingsGroup(c, zhOr(zh, "通知", "Notifications"),
		zhOr(zh, "手机后台通知需要在发送端配置 Miya 的 APNs 密钥，并在手机允许通知。",
			"Phone notifications in the background need Miya's APNs key on the sending side and permission on the phone."),
		rows...)
}
