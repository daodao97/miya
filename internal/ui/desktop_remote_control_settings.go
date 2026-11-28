package desktopui

import (
	"fmt"
	"runtime"
	"time"

	"github.com/egoist/mygo/plugins/permissionflow"
	"github.com/egoist/mygo/ui"

	"miya/internal/desktop"
	svcclient "miya/internal/service/client"
	"miya/internal/widgets"
)

const remotePermissionCheckInterval = 3 * time.Second

type remoteControlUI struct {
	client             *svcclient.Client
	connected          bool
	localMac           bool
	permissions        *desktop.Permissions
	accessibilityGrant permissionflow.Status
	nextCheck          time.Time
	checking           bool
	err                error
	settingsErr        error
}

func (app *App) refreshRemotePermissions() {
	if s := app.remoteControl; s != nil {
		s.nextCheck = time.Time{}
	}
}

func (app *App) checkRemotePermissions(s *remoteControlUI, now time.Time) {
	s.checking, s.nextCheck = true, now.Add(remotePermissionCheckInterval)
	go func() {
		var permissions *desktop.Permissions
		var err error
		if s.client == nil {
			local := desktop.CheckPermissions()
			permissions = &local
		} else {
			err = s.client.Call(desktop.PermissionStatusOp, nil, &permissions)
		}
		var grant permissionflow.Status
		if s.localMac && permissions != nil && permissions.Supported {
			grant = desktop.CheckAccessibilityGrant()
		}
		app.C.Dispatch(func() {
			s.permissions, s.err = permissions, err
			s.accessibilityGrant = grant
			s.checking = false
		})
	}()
}

func (s *remoteControlUI) accessibilityStatus() (status permissionflow.Status, restartRequired bool) {
	active := s.permissions.Accessibility
	switch s.accessibilityGrant {
	case permissionflow.Granted:
		return permissionflow.Granted, active == permissionflow.Denied || active == permissionflow.NotDetermined
	case permissionflow.Denied:
		return permissionflow.Denied, false
	default:
		return active, false
	}
}

func (app *App) remoteControlSettings(c *ui.Context) {
	cl := app.C.State().Remote
	connected := cl == nil || cl.Connected()
	localMac := runtime.GOOS == "darwin" && !app.C.RemoteHost()
	s := app.remoteControl
	if s == nil || s.client != cl || s.connected != connected || s.localMac != localMac {
		s = &remoteControlUI{client: cl, connected: connected, localMac: localMac}
		app.remoteControl = s
	}
	if connected && !s.checking {
		now := c.Now()
		if !now.Before(s.nextCheck) {
			app.checkRemotePermissions(s, now)
		} else {
			c.After(s.nextCheck.Sub(now))
		}
	}

	status, color := "", widgets.Muted
	switch {
	case !connected:
		status = tr("Background service disconnected")
	case s.err != nil && s.err.Error() == fmt.Sprintf("unknown operation %q", desktop.PermissionStatusOp):
		status = tr("Permission checks will be available after the background service updates.")
	case s.err != nil || s.permissions == nil && !s.checking:
		status, color = tr("Could not check remote control permissions."), widgets.Danger
	case s.permissions == nil:
		status = tr("Checking…")
	case !s.permissions.Supported:
		status = tr("Remote desktop requires macOS 14 or later")
	}

	actions := func() {
		if widgets.SmallButton(c.Key("check-remote-permissions"), tr("Check again"), !connected || s.checking).Clicked() {
			app.checkRemotePermissions(s, c.Now())
		}
	}
	if status != "" {
		widgets.SettingsGroupActions(c, tr("Remote Control"), "", actions, func() {
			ui.Text(c, status).FontSize(13).TextColor(color)
		})
		return
	}
	accessibility, restartRequired := s.accessibilityStatus()
	note := ""
	if restartRequired {
		if cl == nil {
			note = tr("Accessibility is enabled in System Settings. Reopen Miya to apply it.")
		} else {
			note = tr("Accessibility is enabled in System Settings. In General, restart the background service when idle to apply it.")
		}
	} else if s.permissions.ScreenRecording != permissionflow.Granted || accessibility != permissionflow.Granted {
		switch {
		case !localMac:
			note = tr("Manage permissions in System Settings on the Mac you want to control.")
		case s.permissions.ScreenRecording == permissionflow.Granted:
			note = tr("Enable Miya in System Settings. If its switch is already on, remove the old entry and add the installed Miya app again.")
		case cl == nil:
			note = tr("Allow Miya in System Settings. After granting screen recording, macOS may ask you to reopen Miya.")
		default:
			note = tr("Allow Miya in System Settings. If the screen recording permission has yet to take effect, restart the background service from General.")
		}
	}
	widgets.SettingsGroupActions(c, tr("Remote Control"), note, actions, func() {
		remotePermissionRow(c, s, permissionflow.ScreenRecording, tr("Screen Recording"), tr("Share the Mac screen with your phone."), s.permissions.ScreenRecording, false)
	}, func() {
		remotePermissionRow(c, s, permissionflow.Accessibility, tr("Accessibility"), tr("Control the Mac keyboard and pointer from your phone."), accessibility, restartRequired)
	})
	if s.settingsErr != nil {
		ui.Text(c, tr("Could not open System Settings.")).FontSize(12).TextColor(widgets.Danger)
	}
}

func remotePermissionRow(c *ui.Context, s *remoteControlUI, p permissionflow.Permission, title, help string, status permissionflow.Status, restartRequired bool) {
	label, color := tr("Unknown"), widgets.Muted
	switch {
	case restartRequired:
		label, color = tr("Granted · restart required"), widgets.WarnInk
	case status == permissionflow.Granted:
		label, color = tr("Granted"), widgets.Success
	case status == permissionflow.Denied || status == permissionflow.NotDetermined:
		label, color = tr("Not granted"), widgets.WarnInk
	}
	widgets.SettingRow(c.Key(p.String()), title, help, func() {
		ui.Text(c, label).FontSize(12).TextColor(color)
		if s.localMac && status != permissionflow.Granted {
			if widgets.SmallButton(c.Key("open-permission-settings"), tr("Open System Settings"), false).Clicked() {
				s.settingsErr = permissionflow.OpenSettings(p)
			}
		}
	})
}
