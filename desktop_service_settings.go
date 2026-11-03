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

func serviceConnected(cl *serviceClient) bool {
	if cl == nil || cl.conn == nil {
		return false
	}
	select {
	case <-cl.closed:
		return false
	default:
		return true
	}
}

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
				s.requested, s.err = false, zhOr(app.c.locale() == "zh", "后台服务尚未退出，请重新尝试重启。", "The background service has not exited. Try restarting again.")
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
	zh := app.c.locale() == "zh"
	if s.requested {
		c.After(time.Second)
	}
	settingsGroup(c, zhOr(zh, "后台服务", "Background service"), "", func() {
		help := zhOr(zh, "重启后窗口会自动重连，未发送的草稿会保留。", "The window reconnects automatically and keeps unsent drafts.")
		if app.c.remote == nil {
			help = zhOr(zh, "当前为独立运行模式，没有单独的后台服务。", "Standalone mode has no separate background service.")
		}
		settingRow(c, zhOr(zh, "重启后台服务", "Restart background service"), help, func() {
			label := zhOr(zh, "重启", "Restart")
			if s.checking {
				label = zhOr(zh, "处理中…", "Working…")
			}
			if smallButton(c.Key("restart-server"), label, !serviceConnected(app.c.remote) || s.checking || s.confirming || s.requested).Clicked() {
				app.checkServiceRestart()
			}
		})
		if s.confirming {
			ui.Text(c, zhOr(zh, "后台仍有任务运行。立即重启将中断所有项目的运行任务，并暂时断开手机连接。", "Background tasks are still running. Restarting now interrupts tasks across all projects and briefly disconnects phones.")).FontSize(12).TextColor(warnInk).LineHeight(1.5)
			ui.Row(c).Wrap().Gap(8).Children(func() {
				if smallButton(c.Key("cancel-restart"), zhOr(zh, "取消", "Cancel"), false).Clicked() {
					s.confirming = false
				}
				if smallButton(c.Key("restart-idle"), zhOr(zh, "空闲后重启", "Restart when idle"), false).Clicked() {
					app.sendServiceRestart(true)
				}
				if smallButton(c.Key("restart-now"), zhOr(zh, "立即重启", "Restart now"), false).TextColor(danger).Clicked() {
					app.sendServiceRestart(false)
				}
			})
		} else if s.err != "" {
			ui.Text(c, zhOr(zh, "重启失败：", "Restart failed: ")+s.err).FontSize(12).TextColor(danger)
		} else if s.requested {
			text := zhOr(zh, "正在重启后台服务，等待自动重连…", "Restarting the background service; waiting to reconnect…")
			if s.waitIdle {
				text = zhOr(zh, "已安排重启，等待后台任务结束后自动重连。", "Restart scheduled. The window will reconnect after background tasks finish.")
			}
			ui.Text(c, text).FontSize(12).TextColor(muted)
			if s.waitIdle && !s.checking && serviceConnected(s.client) {
				if smallButton(c.Key("override-restart"), zhOr(zh, "立即重启…", "Restart now…"), false).Clicked() {
					s.confirming = true
				}
			}
		} else if s.completed {
			ui.Text(c, zhOr(zh, "后台服务已重启并重新连接。", "Background service restarted and reconnected.")).FontSize(12).TextColor(success)
		}
	})
}
