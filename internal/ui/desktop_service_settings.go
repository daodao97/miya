package desktopui

import (
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/svcproto"
	"miya/internal/widgets"
)

type ServiceRestartUI struct {
	Client     *svcproto.Client
	Pid        int
	Checking   bool
	Confirming bool
	Requested  bool
	waitIdle   bool
	Completed  bool
	Err        string
}

func ServiceConnected(cl *svcproto.Client) bool { return cl.Connected() }

func (app *App) checkServiceRestart() {
	cl := app.C.State().Remote
	if app.ServiceRestart.Checking || !ServiceConnected(cl) {
		return
	}
	app.ServiceRestart = ServiceRestartUI{Client: cl, Checking: true}
	go func() {
		var h svcproto.Hello
		err := cl.Call("hello", nil, &h)
		app.C.Dispatch(func() {
			s := &app.ServiceRestart
			s.Checking = false
			if app.C.State().Remote != cl && err == nil {
				err = svcproto.ErrClosed
			}
			if err != nil {
				s.Err = err.Error()
				return
			}
			s.Pid = h.PID
			if h.Busy {
				s.Confirming = true
			} else {
				// Recheck idleness at the service: work may have started since hello.
				app.sendServiceRestart(true)
			}
		})
	}()
}

func (app *App) sendServiceRestart(waitIdle bool) {
	s := &app.ServiceRestart
	cl := s.Client
	if s.Checking {
		return
	}
	if cl != app.C.State().Remote || !ServiceConnected(cl) {
		s.Confirming, s.Requested, s.Err = false, false, svcproto.ErrClosed.Error()
		return
	}
	s.Confirming, s.Checking, s.Requested, s.waitIdle, s.Err = false, true, true, waitIdle, ""
	op := "quit"
	if waitIdle {
		op = "quitWhenIdle"
	}
	go func() {
		err := cl.Call(op, nil, nil)
		app.C.Dispatch(func() {
			s := &app.ServiceRestart
			s.Checking = false
			// Shutdown can close the socket before its reply is written. The
			// reconnect check below must still prove that a new process started.
			if err != nil && ServiceConnected(cl) {
				s.Requested, s.Err = false, err.Error()
			}
		})
	}()
}

// Reconnection itself belongs to watchService, and must work even after the
// user leaves Settings. Verify a different PID before reporting completion.
func (app *App) updateServiceRestart() {
	s := &app.ServiceRestart
	cl := app.C.State().Remote
	if !s.Requested || s.Checking || cl == s.Client || !ServiceConnected(cl) {
		return
	}
	s.Checking = true
	go func() {
		var h svcproto.Hello
		err := cl.Call("hello", nil, &h)
		app.C.Dispatch(func() {
			s := &app.ServiceRestart
			s.Checking = false
			if cl != app.C.State().Remote {
				return // another reconnect is already in progress
			}
			if err != nil {
				s.Requested, s.Err = false, err.Error()
				return
			}
			if h.PID != s.Pid && h.PID > 0 {
				s.Confirming, s.Requested, s.Completed = false, false, true
			} else {
				s.Requested, s.Err = false, tr("The background service has not exited. Try restarting again.")
			}
		})
	}()
}

func (app *App) ServiceSettings(c *ui.Context) {
	if app.Mobile {
		return
	}
	app.updateServiceRestart()
	s := &app.ServiceRestart
	if s.Requested {
		c.After(time.Second)
	}
	widgets.SettingsGroup(c, tr("Background service"), "", func() {
		help := tr("The window reconnects automatically and keeps unsent drafts.")
		if app.C.State().Remote == nil {
			help = tr("Standalone mode has no separate background service.")
		}
		widgets.SettingRow(c, tr("Restart background service"), help, func() {
			label := tr("Restart")
			if s.Checking {
				label = tr("Working…")
			}
			if widgets.SmallButton(c.Key("restart-server"), label, !ServiceConnected(app.C.State().Remote) || s.Checking || s.Confirming || s.Requested).Clicked() {
				app.checkServiceRestart()
			}
		})
		if s.Confirming {
			ui.Text(c, tr("Background tasks are still running. Restarting now interrupts tasks across all projects and briefly disconnects phones.")).FontSize(12).TextColor(widgets.WarnInk).LineHeight(1.5)
			ui.Row(c).Wrap().Gap(8).Children(func() {
				if widgets.SmallButton(c.Key("cancel-restart"), tr("Cancel"), false).Clicked() {
					s.Confirming = false
				}
				if widgets.SmallButton(c.Key("restart-idle"), tr("Restart when idle"), false).Clicked() {
					app.sendServiceRestart(true)
				}
				if widgets.SmallButton(c.Key("restart-now"), tr("Restart now"), false).TextColor(widgets.Danger).Clicked() {
					app.sendServiceRestart(false)
				}
			})
		} else if s.Err != "" {
			ui.Text(c, tr("Restart failed: ")+s.Err).FontSize(12).TextColor(widgets.Danger)
		} else if s.Requested {
			text := tr("Restarting the background service; waiting to reconnect…")
			if s.waitIdle {
				text = tr("Restart scheduled. The window will reconnect after background tasks finish.")
			}
			ui.Text(c, text).FontSize(12).TextColor(widgets.Muted)
			if s.waitIdle && !s.Checking && ServiceConnected(s.Client) {
				if widgets.SmallButton(c.Key("override-restart"), tr("Restart now…"), false).Clicked() {
					s.Confirming = true
				}
			}
		} else if s.Completed {
			ui.Text(c, tr("Background service restarted and reconnected.")).FontSize(12).TextColor(widgets.Success)
		}
	})
}
