package widgets

import (
	appmodel "miya/internal/appmodel"

	ui "github.com/egoist/mygo/ui"
)

func NotificationSettings(c *ui.Context, _ bool) {
	options := []struct{ key, en, enHelp string }{
		{"completion", tr("Task finished"), tr("Notify when a conversation finishes its turn.")},
		{"waiting", tr("Waiting for approval"), tr("Notify when an action needs your approval.")},
	}
	rows := make([]func(), 0, len(options))
	for _, option := range options {
		option := option
		rows = append(rows, func() {
			label := option.en
			enabled := appmodel.LoadSetting("notifications-"+option.key) != "off"
			SettingRow(c, label, option.enHelp, func() {
				on := enabled
				ui.Switch(c.Key(option.key), &on).Label(label)
				if on != enabled {
					value := "off"
					if on {
						value = "on"
					}
					appmodel.PersistSetting("notifications-"+option.key, value)
				}
			})
		})
	}
	SettingsGroup(c, tr("Notifications"),
		tr("Phone notifications in the background need Miya's APNs key on the sending side and permission on the phone."),
		rows...)
}
