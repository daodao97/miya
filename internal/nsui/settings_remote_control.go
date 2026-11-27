package nsui

import (
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/desktop"
)

func remoteControlSettings(cx *core.Scope, c Host) ui.SettingsGroup {
	return ui.SettingsGroup{ID: "remote-control", Title: tr("Remote control"), Items: []ui.SettingsItem{{
		ID: "remote-control-permissions", Content: core.Func(func(cx *core.Scope) core.Node {
			permissions := core.NewSignal(cx, desktop.Permissions{})
			message := core.NewSignal(cx, "")
			refresh := func() {
				if !c.RemoteHost() {
					permissions.Set(desktop.CheckPermissions())
					return
				}
				cl := c.State().Remote
				if cl == nil || !cl.Connected() {
					return
				}
				go func() {
					var p desktop.Permissions
					err := cl.Call(desktop.PermissionStatusOp, nil, &p)
					cx.Post(func() {
						if c.State().Remote != cl {
							return
						}
						if err != nil {
							message.Set(err.Error())
							return
						}
						permissions.Set(p)
						message.Set("")
					})
				}()
			}
			cx.Post(refresh)
			row := func(title string, key core.Permission, get func(desktop.Permissions) desktop.PermissionStatus) core.Node {
				return settingsActions(core.Text{Content: tr(title)}, settingsText(cx, func() string {
					switch get(permissions.Get()) {
					case desktop.PermissionGranted:
						return tr("Allowed")
					case desktop.PermissionDenied:
						return tr("Permission required")
					}
					return tr("Unknown")
				}), core.Show{When: func() bool { return !c.RemoteHost() && get(permissions.Get()) != desktop.PermissionGranted }, Then: func(cx *core.Scope) core.Node {
					return settingsButton("", tr("Open System Settings"), func() { cx.OpenPermissionSettings(key, nil) }, nil)
				}})
			}
			return core.Show{When: func() bool { return permissions.Get().Supported }, Then: func(cx *core.Scope) core.Node {
				return settingsColumn(
					row("Screen Recording", core.PermissionScreenRecording, func(p desktop.Permissions) desktop.PermissionStatus { return p.ScreenRecording }),
					row("Accessibility", core.PermissionAccessibility, func(p desktop.Permissions) desktop.PermissionStatus { return p.Accessibility }),
					settingsButton("remote-control-refresh", tr("Refresh"), refresh, nil), settingsText(cx, message.Get),
				)
			}}
		}),
	}}}
}
