package desktopui

import (
	"time"

	"github.com/egoist/mygo/ui"

	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
	"miya/internal/widgets"
)

// RestartPhase is where a background service restart stands. Exactly one
// phase holds at a time; the hello and quit calls run between phases.
type RestartPhase uint8

const (
	RestartIdle       RestartPhase = iota
	RestartChecking                // hello: is the service busy?
	RestartConfirming              // busy: the person chooses now, when idle, or cancel
	RestartRequesting              // quit or quitWhenIdle is in flight
	RestartWaiting                 // the service agreed to exit; watchService reconnects
	RestartVerifying               // reconnected: hello must report a different PID
	RestartDone
	RestartFailed
)

// Calling reports whether a service call for this restart is in flight.
func (p RestartPhase) Calling() bool {
	return p == RestartChecking || p == RestartRequesting || p == RestartVerifying
}

type ServiceRestartUI struct {
	Client *svcclient.Client
	Pid    int
	Phase  RestartPhase
	// back is where Cancel in RestartConfirming returns: RestartIdle, or
	// RestartWaiting when the person is overriding a scheduled restart.
	back RestartPhase
	// whenIdle is true when the last request asked the service to exit
	// only after its tasks finish.
	whenIdle bool
	Err      string
}

// Pending reports whether a restart has been requested and not yet settled.
func (s *ServiceRestartUI) Pending() bool {
	switch s.Phase {
	case RestartRequesting, RestartWaiting, RestartVerifying:
		return true
	case RestartConfirming:
		return s.back == RestartWaiting
	}
	return false
}

func (s *ServiceRestartUI) fail(err string) { s.Phase, s.Err = RestartFailed, err }

func ServiceConnected(cl *svcclient.Client) bool { return cl.Connected() }

func (app *App) checkServiceRestart() {
	cl := app.C.State().Remote
	if app.ServiceRestart.Phase.Calling() || !ServiceConnected(cl) {
		return
	}
	app.ServiceRestart = ServiceRestartUI{Client: cl, Phase: RestartChecking}
	go func() {
		var h svcproto.Hello
		err := cl.Call("hello", nil, &h)
		app.C.Dispatch(func() {
			s := &app.ServiceRestart
			if s.Phase != RestartChecking || s.Client != cl {
				return
			}
			if app.C.State().Remote != cl && err == nil {
				err = svcproto.ErrClosed
			}
			if err != nil {
				s.fail(err.Error())
				return
			}
			s.Pid = h.PID
			if h.Busy {
				s.Phase, s.back = RestartConfirming, RestartIdle
				return
			}
			// Recheck idleness at the service: work may have started since hello.
			s.Phase = RestartIdle
			app.sendServiceRestart(true)
		})
	}()
}

func (app *App) sendServiceRestart(whenIdle bool) {
	s := &app.ServiceRestart
	cl := s.Client
	if s.Phase.Calling() {
		return
	}
	if cl != app.C.State().Remote || !ServiceConnected(cl) {
		s.fail(svcproto.ErrClosed.Error())
		return
	}
	s.Phase, s.whenIdle, s.Err = RestartRequesting, whenIdle, ""
	op := "quit"
	if whenIdle {
		op = "quitWhenIdle"
	}
	go func() {
		err := cl.Call(op, nil, nil)
		app.C.Dispatch(func() {
			s := &app.ServiceRestart
			if s.Phase != RestartRequesting || s.Client != cl {
				return
			}
			// Shutdown can close the socket before its reply is written. The
			// reconnect check must still prove that a new process started.
			if err != nil && ServiceConnected(cl) {
				s.fail(err.Error())
				return
			}
			s.Phase = RestartWaiting
		})
	}()
}

// Reconnection itself belongs to watchService, and must work even after the
// user leaves Settings. Verify a different PID before reporting completion.
func (app *App) updateServiceRestart() {
	s := &app.ServiceRestart
	cl := app.C.State().Remote
	waiting := s.Phase == RestartWaiting || s.Phase == RestartConfirming && s.back == RestartWaiting
	if !waiting || cl == s.Client || !ServiceConnected(cl) {
		return
	}
	// The old service is gone, so an override choice no longer applies.
	s.Phase = RestartVerifying
	go func() {
		var h svcproto.Hello
		err := cl.Call("hello", nil, &h)
		app.C.Dispatch(func() {
			s := &app.ServiceRestart
			if s.Phase != RestartVerifying {
				return
			}
			if cl != app.C.State().Remote {
				s.Phase = RestartWaiting // another reconnect is already in progress
				return
			}
			switch {
			case err != nil:
				s.fail(err.Error())
			case h.PID != s.Pid && h.PID > 0:
				s.Phase = RestartDone
			default:
				s.fail(tr("The background service has not exited. Try restarting again."))
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
	if s.Pending() {
		c.After(time.Second)
	}
	widgets.SettingsGroup(c, tr("Background service"), "", func() {
		help := tr("The window reconnects automatically and keeps unsent drafts.")
		if app.C.State().Remote == nil {
			help = tr("Standalone mode has no separate background service.")
		}
		widgets.SettingRow(c, tr("Restart background service"), help, func() {
			label := tr("Restart")
			if s.Phase.Calling() {
				label = tr("Working…")
			}
			settled := s.Phase == RestartIdle || s.Phase == RestartDone || s.Phase == RestartFailed
			if widgets.SmallButton(c.Key("restart-server"), label, !ServiceConnected(app.C.State().Remote) || !settled).Clicked() {
				app.checkServiceRestart()
			}
		})
		switch s.Phase {
		case RestartConfirming:
			ui.Text(c, tr("Background tasks are still running. Restarting now interrupts tasks across all projects and briefly disconnects phones.")).FontSize(12).TextColor(widgets.WarnInk).LineHeight(1.5)
			ui.Row(c).Wrap().Gap(8).Children(func() {
				if widgets.SmallButton(c.Key("cancel-restart"), tr("Cancel"), false).Clicked() {
					s.Phase = s.back
				}
				if widgets.SmallButton(c.Key("restart-idle"), tr("Restart when idle"), false).Clicked() {
					app.sendServiceRestart(true)
				}
				if widgets.SmallButton(c.Key("restart-now"), tr("Restart now"), false).TextColor(widgets.Danger).Clicked() {
					app.sendServiceRestart(false)
				}
			})
		case RestartFailed:
			ui.Text(c, tr("Restart failed: ")+s.Err).FontSize(12).TextColor(widgets.Danger)
		case RestartRequesting, RestartWaiting, RestartVerifying:
			text := tr("Restarting the background service; waiting to reconnect…")
			if s.whenIdle {
				text = tr("Restart scheduled. The window will reconnect after background tasks finish.")
			}
			ui.Text(c, text).FontSize(12).TextColor(widgets.Muted)
			if s.Phase == RestartWaiting && s.whenIdle && ServiceConnected(s.Client) {
				if widgets.SmallButton(c.Key("override-restart"), tr("Restart now…"), false).Clicked() {
					s.Phase, s.back = RestartConfirming, RestartWaiting
				}
			}
		case RestartDone:
			ui.Text(c, tr("Background service restarted and reconnected.")).FontSize(12).TextColor(widgets.Success)
		}
	})
}
