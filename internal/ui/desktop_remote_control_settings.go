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
	client      *svcclient.Client
	connected   bool
	permissions *desktop.Permissions
	checkedAt   time.Time
	checking    bool
	err         error
	settingsErr error
}

func (app *App) checkRemotePermissions(s *remoteControlUI) {
	s.checking = true
	go func() {
		var permissions *desktop.Permissions
		var err error
		if s.client == nil {
			local := desktop.CheckPermissions()
			permissions = &local
		} else {
			err = s.client.Call(desktop.PermissionStatusOp, nil, &permissions)
		}
		app.C.Dispatch(func() {
			s.permissions, s.err = permissions, err
			s.checking, s.checkedAt = false, time.Now()
		})
	}()
}

func (app *App) remoteControlSettings(c *ui.Context) {
	cl := app.C.State().Remote
	connected := cl == nil || cl.Connected()
	s := app.remoteControl
	if s == nil || s.client != cl || s.connected != connected {
		s = &remoteControlUI{client: cl, connected: connected}
		app.remoteControl = s
	}
	if connected && !s.checking && time.Since(s.checkedAt) >= remotePermissionCheckInterval {
		app.checkRemotePermissions(s)
	}
	c.After(remotePermissionCheckInterval)

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
			app.checkRemotePermissions(s)
		}
	}
	if status != "" {
		widgets.SettingsGroupActions(c, tr("Remote Control"), "", actions, func() {
			ui.Text(c, status).FontSize(13).TextColor(color)
		})
		return
	}
	canOpenSettings := runtime.GOOS == "darwin" && !app.C.RemoteHost()
	note := ""
	if s.permissions.ScreenRecording != permissionflow.Granted || s.permissions.Accessibility != permissionflow.Granted {
		switch {
		case !canOpenSettings:
			note = tr("Manage permissions in System Settings on the Mac you want to control.")
		case cl == nil:
			note = tr("Allow Miya in System Settings. After granting screen recording, macOS may ask you to reopen Miya.")
		default:
			note = tr("Allow Miya in System Settings. If the screen recording permission has yet to take effect, restart the background service from General.")
		}
	}
	widgets.SettingsGroupActions(c, tr("Remote Control"), note, actions, func() {
		remotePermissionRow(c, s, permissionflow.ScreenRecording, tr("Screen Recording"), tr("Share the Mac screen with your phone."), s.permissions.ScreenRecording, canOpenSettings)
	}, func() {
		remotePermissionRow(c, s, permissionflow.Accessibility, tr("Accessibility"), tr("Control the Mac keyboard and pointer from your phone."), s.permissions.Accessibility, canOpenSettings)
	})
	if s.settingsErr != nil {
		ui.Text(c, tr("Could not open System Settings.")).FontSize(12).TextColor(widgets.Danger)
	}
}

func remotePermissionRow(c *ui.Context, s *remoteControlUI, p permissionflow.Permission, title, help string, status permissionflow.Status, canOpenSettings bool) {
	label, color := tr("Unknown"), widgets.Muted
	switch status {
	case permissionflow.Granted:
		label, color = tr("Granted"), widgets.Success
	case permissionflow.Denied, permissionflow.NotDetermined:
		label, color = tr("Not granted"), widgets.WarnInk
	}
	widgets.SettingRow(c.Key(p.String()), title, help, func() {
		ui.Text(c, label).FontSize(12).TextColor(color)
		if canOpenSettings && status != permissionflow.Granted {
			if widgets.SmallButton(c.Key("open-permission-settings"), tr("Open System Settings"), false).Clicked() {
				s.settingsErr = permissionflow.OpenSettings(p)
			}
		}
	})
}
