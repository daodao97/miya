package desktopui

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/selfupdate"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
	"miya/internal/widgets"
)

// MiyaUpdateUI belongs to one workspace. Reconnecting may change its client,
// but cannot change which target version the user asked to install.
type MiyaUpdateUI struct {
	Client   *svcclient.Client
	Status   selfupdate.Status
	Calling  bool
	Wanted   string
	PID      int
	NextPoll time.Time
	Err      string
}

func (app *App) syncMiyaUpdate(c *ui.Context) {
	if !app.C.RemoteHost() {
		return
	}
	u := &app.MiyaUpdate
	cl := app.C.State().Remote
	if !ServiceConnected(cl) {
		return
	}
	if cl != u.Client {
		u.Client, u.Calling, u.Err = cl, true, ""
		go func() {
			var h svcproto.Hello
			err := cl.Call("hello", nil, &h)
			app.C.Dispatch(func() {
				if app.C.State().Remote != cl || app.MiyaUpdate.Client != cl {
					return
				}
				u := &app.MiyaUpdate
				u.Calling = false
				if err != nil {
					u.Err = err.Error()
					return
				}
				if u.Wanted != "" {
					if h.Release == u.Wanted && h.PID > 0 && h.PID != u.PID {
						u.Status = selfupdate.Status{Supported: h.SelfUpdate, Current: h.Release, Phase: "done"}
						u.Wanted = ""
						return
					}
					// A different process alone does not prove the release changed.
					u.Err = tr("The target has reconnected, but the requested Miya version is not running yet.")
				}
				u.PID = h.PID
				if !h.SelfUpdate {
					u.Status = selfupdate.Status{Current: h.Release, Phase: "unsupported"}
					return
				}
				// Another paired client may already be installing. Read that
				// state before starting a check, so reconnecting never hides an
				// existing installation behind an "already in progress" error.
				app.callMiyaUpdate("status")
			})
		}()
	}
	if u.Calling || u.Status.Phase == "checking" || u.Status.Phase == "installing" || u.Status.Phase == "waiting" {
		c.After(time.Second)
		if !u.Calling && time.Now().After(u.NextPoll) {
			app.callMiyaUpdate("status")
		}
	}
}

func (app *App) callMiyaUpdate(action string) {
	u := &app.MiyaUpdate
	cl := app.C.State().Remote
	if u.Calling || cl != u.Client || !ServiceConnected(cl) {
		return
	}
	version := u.Status.Latest
	if action == "install" {
		u.Wanted = version
	}
	u.Calling = true
	if action != "status" {
		u.Err = ""
	}
	go func() {
		var status selfupdate.Status
		err := cl.Call("miyaUpdate", struct{ Action, Version string }{action, version}, &status)
		app.C.Dispatch(func() {
			if app.MiyaUpdate.Client != cl || app.C.State().Remote != cl {
				return
			}
			u := &app.MiyaUpdate
			u.Calling, u.NextPoll = false, time.Now().Add(time.Second)
			if err != nil {
				u.Err = err.Error()
				return
			}
			u.Status = status
			if u.Wanted == "" && (status.Phase == "installing" || status.Phase == "waiting") {
				u.Wanted = status.Latest
			}
			if status.Phase == "failed" {
				u.Wanted = ""
			}
			if action == "status" && status.Phase == "idle" && u.Wanted == "" && status.Supported {
				app.callMiyaUpdate("check")
			}
		})
	}()
}

func (app *App) MiyaUpdateSettings(c *ui.Context) {
	if !app.C.RemoteHost() {
		return
	}
	app.syncMiyaUpdate(c)
	u := &app.MiyaUpdate
	widgets.SettingsGroup(c, tr("Miya on this workspace"), "", func() {
		ui.Text(c, fmt.Sprintf(tr("Running version: %s"), u.Status.Current)).FontSize(12).TextColor(widgets.Muted)
		switch u.Status.Phase {
		case "unsupported":
			widgets.MutedText(c, tr("Install an updated Miya on the target computer once to enable remote upgrades."))
		case "available":
			ui.Text(c, fmt.Sprintf(tr("Available version: %s"), u.Status.Latest)).FontSize(12)
			widgets.MutedText(c, tr("The target downloads and verifies the release, keeps a backup, and restarts after running tasks finish."))
			if widgets.SmallButton(c.Key("install-miya-update"), tr("Upgrade target"), u.Calling).Clicked() {
				app.callMiyaUpdate("install")
			}
		case "checking":
			widgets.MutedText(c, tr("Checking for Miya updates…"))
		case "installing":
			widgets.MutedText(c, tr("Downloading and verifying Miya on the target…"))
		case "waiting":
			widgets.MutedText(c, tr("Update installed. Waiting for tasks to finish and the new background service to reconnect…"))
		case "installed":
			widgets.MutedText(c, tr("Update installed. Reopen Miya on the target computer to finish switching versions."))
		case "done":
			ui.Text(c, tr("The target is running the requested Miya version.")).FontSize(12).TextColor(widgets.Success)
		case "current":
			widgets.MutedText(c, tr("Miya is up to date on this workspace."))
		}
		if u.Status.Backup != "" {
			widgets.MutedText(c, fmt.Sprintf(tr("Backup on target: %s"), u.Status.Backup))
		}
		if u.Status.Error != "" {
			ui.Text(c, u.Status.Error).FontSize(12).TextColor(widgets.Danger)
		}
		if u.Err != "" {
			ui.Text(c, u.Err).FontSize(12).TextColor(widgets.Danger)
		}
		busy := u.Calling || u.Status.Phase == "checking" || u.Status.Phase == "installing" || u.Status.Phase == "waiting" || u.Status.Phase == "installed" || !u.Status.Supported
		if widgets.SmallButton(c.Key("check-miya-update"), tr("Check for Miya updates"), busy).Clicked() {
			app.callMiyaUpdate("check")
		}
	})
}
