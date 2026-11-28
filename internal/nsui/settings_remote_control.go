package nsui

import (
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/desktop"
)

func remoteControlSettings(c Host) ui.SettingsGroup {
	return ui.SettingsGroup{ID: "remote-control", Items: []ui.SettingsItem{{
		ID: "remote-control-permissions", Title: tr("Remote control"), Keywords: []string{tr("Screen Recording"), tr("Accessibility")},
		Content: core.Func(func(cx *core.Scope) core.Node {
			permissions := core.NewSignal[*desktop.Permissions](cx, nil)
			message := core.NewSignal(cx, "")
			openError := core.NewSignal(cx, "")
			checking := core.NewSignal(cx, false)
			refresh := func() {
				if checking.Get() {
					return
				}
				if !c.RemoteHost() {
					p := desktop.CheckPermissions()
					permissions.Set(&p)
					message.Set("")
					return
				}
				cl := c.State().Remote
				if cl == nil || !cl.Connected() {
					message.Set(tr("Background service disconnected"))
					permissions.Set(nil)
					return
				}
				checking.Set(true)
				go func() {
					var p desktop.Permissions
					err := cl.Call(desktop.PermissionStatusOp, nil, &p)
					cx.Post(func() {
						checking.Set(false)
						if c.State().Remote != cl {
							permissions.Set(nil)
							return
						}
						if err != nil {
							message.Set(err.Error())
							permissions.Set(nil)
							return
						}
						permissions.Set(&p)
						message.Set("")
					})
				}()
			}
			var poll func()
			poll = func() {
				refresh()
				cx.AfterFunc(3*time.Second, poll)
			}
			cx.Post(poll)
			return settingsColumn(
				settingsFormActions(settingsButton("remote-control-refresh", tr("Check again"), refresh, checking.Get)),
				settingsText(cx, openError.Get),
				core.Dynamic(func(cx *core.Scope) core.Node {
					p := permissions.Get()
					if p == nil {
						return settingsText(cx, func() string { return firstSettingsText(message.Get(), tr("Checking…")) })
					}
					if !p.Supported {
						return settingsText(cx, func() string { return tr("Remote desktop requires macOS 14 or later") })
					}
					row := func(title, detail string, key core.Permission, status desktop.PermissionStatus) core.Node {
						label := tr("Unknown")
						switch status {
						case desktop.PermissionGranted:
							label = tr("Allowed")
						case desktop.PermissionDenied:
							label = tr("Permission required")
						}
						var action core.Node
						if !c.RemoteHost() && status != desktop.PermissionGranted {
							action = settingsButton("", tr("Open System Settings"), func() {
								cx.OpenPermissionSettings(key, func(opened bool) {
									msg := ""
									if !opened {
										msg = tr("Could not open System Settings.") + " (" + tr(title) + ")"
									}
									openError.Set(msg)
								})
							}, nil)
						}
						id := "settings-permission-screen"
						if key == core.PermissionAccessibility {
							id = "settings-permission-accessibility"
						}
						return ui.FormRow{TestID: id, Title: tr(title), Detail: tr(detail) + "\n" + label, Control: action}
					}
					help := ""
					if p.ScreenRecording != desktop.PermissionGranted || p.Accessibility != desktop.PermissionGranted {
						help = tr("Allow Miya in System Settings. After granting screen recording, macOS may ask you to reopen Miya.")
						if c.RemoteHost() {
							help = tr("Manage permissions in System Settings on the Mac you want to control.")
						}
					}
					return ui.FormSection{Footer: help, Children: []core.Node{
						row("Screen Recording", "Share the Mac screen with your phone.", core.PermissionScreenRecording, p.ScreenRecording),
						row("Accessibility", "Control the Mac keyboard and pointer from your phone.", core.PermissionAccessibility, p.Accessibility),
					}}
				}),
			)
		}),
	}}}
}
