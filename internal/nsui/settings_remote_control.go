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
			grant := core.NewSignal(cx, desktop.PermissionUnknown)
			message := core.NewSignal(cx, "")
			openError := core.NewSignal(cx, "")
			checking := core.NewSignal(cx, false)
			refresh := func() {
				if checking.Get() {
					return
				}
				remote := c.RemoteHost()
				cl := c.State().Remote
				if remote && (cl == nil || !cl.Connected()) {
					message.Set(tr("Background service disconnected"))
					permissions.Set(nil)
					return
				}
				checking.Set(true)
				go func() {
					var p desktop.Permissions
					var err error
					if cl != nil && cl.Connected() {
						err = cl.Call(desktop.PermissionStatusOp, nil, &p)
					} else if !remote {
						p = desktop.CheckPermissions()
					}
					fresh := desktop.PermissionUnknown
					if err == nil && !remote && p.Supported {
						fresh = desktop.CheckAccessibilityGrant()
					}
					cx.Post(func() {
						checking.Set(false)
						if c.State().Remote != cl || c.RemoteHost() != remote {
							permissions.Set(nil)
							return
						}
						if err != nil {
							message.Set(err.Error())
							permissions.Set(nil)
							return
						}
						grant.Set(fresh)
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
					accessibility, restart := p.AccessibilityStatus(grant.Get())
					row := func(title, detail string, key core.Permission, status desktop.PermissionStatus, restart bool) core.Node {
						label := tr("Unknown")
						switch status {
						case desktop.PermissionGranted:
							label = tr("Allowed")
						case desktop.PermissionDenied:
							label = tr("Permission required")
						}
						if restart {
							label = tr("Granted · restart required")
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
					if restart {
						help = tr("Accessibility is enabled in System Settings. Reopen Miya to apply it.")
						if c.State().Remote != nil {
							help = tr("Accessibility is enabled in System Settings. In General, restart the background service when idle to apply it.")
						}
					} else if p.ScreenRecording != desktop.PermissionGranted || accessibility != desktop.PermissionGranted {
						help = tr("Allow Miya in System Settings. After granting screen recording, macOS may ask you to reopen Miya.")
						if c.RemoteHost() {
							help = tr("Manage permissions in System Settings on the Mac you want to control.")
						}
					}
					return ui.FormSection{Footer: help, Children: []core.Node{
						row("Screen Recording", "Share the Mac screen with your phone.", core.PermissionScreenRecording, p.ScreenRecording, false),
						row("Accessibility", "Control the Mac keyboard and pointer from your phone.", core.PermissionAccessibility, accessibility, restart),
					}}
				}),
			)
		}),
	}}}
}
