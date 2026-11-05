package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

type serviceRestartUI struct {
	client     *serviceClient
	pid        int
	checking   bool
	confirming bool
	requested  bool
	waitIdle   bool
	completed  bool
	err        string
}

func serviceConnected(cl *serviceClient) bool { return cl.Connected() }

func (app *chatApp) checkServiceRestart() {
	cl := app.c.remote
	if app.serviceRestart.checking || !serviceConnected(cl) {
		return
	}
	app.serviceRestart = serviceRestartUI{client: cl, checking: true}
	go func() {
		var h svcHello
		err := cl.call("hello", nil, &h)
		app.c.dispatch(func() {
			s := &app.serviceRestart
			s.checking = false
			if app.c.remote != cl && err == nil {
				err = errServiceClosed
			}
			if err != nil {
				s.err = err.Error()
				return
			}
			s.pid = h.PID
			if h.Busy {
				s.confirming = true
			} else {
				// Recheck idleness at the service: work may have started since hello.
				app.sendServiceRestart(true)
			}
		})
	}()
}

func (app *chatApp) sendServiceRestart(waitIdle bool) {
	s := &app.serviceRestart
	cl := s.client
	if s.checking {
		return
	}
	if cl != app.c.remote || !serviceConnected(cl) {
		s.confirming, s.requested, s.err = false, false, errServiceClosed.Error()
		return
	}
	s.confirming, s.checking, s.requested, s.waitIdle, s.err = false, true, true, waitIdle, ""
	op := "quit"
	if waitIdle {
		op = "quitWhenIdle"
	}
	go func() {
		err := cl.call(op, nil, nil)
		app.c.dispatch(func() {
			s := &app.serviceRestart
			s.checking = false
			// Shutdown can close the socket before its reply is written. The
			// reconnect check below must still prove that a new process started.
			if err != nil && serviceConnected(cl) {
				s.requested, s.err = false, err.Error()
			}
		})
	}()
}

// Reconnection itself belongs to watchService, and must work even after the
// user leaves Settings. Verify a different PID before reporting completion.
func (app *chatApp) updateServiceRestart() {
	s := &app.serviceRestart
	cl := app.c.remote
	if !s.requested || s.checking || cl == s.client || !serviceConnected(cl) {
		return
	}
	s.checking = true
	go func() {
		var h svcHello
		err := cl.call("hello", nil, &h)
		app.c.dispatch(func() {
			s := &app.serviceRestart
			s.checking = false
			if cl != app.c.remote {
				return // another reconnect is already in progress
			}
			if err != nil {
				s.requested, s.err = false, err.Error()
				return
			}
			if h.PID != s.pid && h.PID > 0 {
				s.confirming, s.requested, s.completed = false, false, true
			} else {
				s.requested, s.err = false, tr("The background service has not exited. Try restarting again.")
			}
		})
	}()
}

func (app *chatApp) serviceSettings(c *ui.Context) {
	if app.mobile {
		return
	}
	app.updateServiceRestart()
	s := &app.serviceRestart
	if s.requested {
		c.After(time.Second)
	}
	settingsGroup(c, tr("Background service"), "", func() {
		help := tr("The window reconnects automatically and keeps unsent drafts.")
		if app.c.remote == nil {
			help = tr("Standalone mode has no separate background service.")
		}
		settingRow(c, tr("Restart background service"), help, func() {
			label := tr("Restart")
			if s.checking {
				label = tr("Working…")
			}
			if smallButton(c.Key("restart-server"), label, !serviceConnected(app.c.remote) || s.checking || s.confirming || s.requested).Clicked() {
				app.checkServiceRestart()
			}
		})
		if s.confirming {
			ui.Text(c, tr("Background tasks are still running. Restarting now interrupts tasks across all projects and briefly disconnects phones.")).FontSize(12).TextColor(warnInk).LineHeight(1.5)
			ui.Row(c).Wrap().Gap(8).Children(func() {
				if smallButton(c.Key("cancel-restart"), tr("Cancel"), false).Clicked() {
					s.confirming = false
				}
				if smallButton(c.Key("restart-idle"), tr("Restart when idle"), false).Clicked() {
					app.sendServiceRestart(true)
				}
				if smallButton(c.Key("restart-now"), tr("Restart now"), false).TextColor(danger).Clicked() {
					app.sendServiceRestart(false)
				}
			})
		} else if s.err != "" {
			ui.Text(c, tr("Restart failed: ")+s.err).FontSize(12).TextColor(danger)
		} else if s.requested {
			text := tr("Restarting the background service; waiting to reconnect…")
			if s.waitIdle {
				text = tr("Restart scheduled. The window will reconnect after background tasks finish.")
			}
			ui.Text(c, text).FontSize(12).TextColor(muted)
			if s.waitIdle && !s.checking && serviceConnected(s.client) {
				if smallButton(c.Key("override-restart"), tr("Restart now…"), false).Clicked() {
					s.confirming = true
				}
			}
		} else if s.completed {
			ui.Text(c, tr("Background service restarted and reconnected.")).FontSize(12).TextColor(success)
		}
	})
}
