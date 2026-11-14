package nsui

import (
	"fmt"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/selfupdate"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
)

// Service work belongs to the dialog's host scope, not its currently visible
// page: scheduling an idle restart must survive closing Settings.
type settingsService struct {
	cx                  *core.Scope
	c                   Host
	revision            *core.Signal[int]
	restartClient       *svcclient.Client
	restartPID          int
	phase, back         string
	whenIdle            bool
	restartError        string
	updateClient        *svcclient.Client
	update              selfupdate.Status
	updateBusy          bool
	updateError, wanted string
	updatePID           int
}

func newSettingsService(cx *core.Scope, c Host) *settingsService {
	s := &settingsService{cx: cx, c: c, revision: core.NewSignal(cx, 0), phase: "idle"}
	cx.Effect(func() {
		cl := Live(func() *svcclient.Client { return c.State().Remote })()
		cx.Untrack(func() {
			if cl != nil && cl.Connected() && (s.phase == "waiting" || s.phase == "confirm" && s.back == "waiting") && cl != s.restartClient {
				s.verifyRestart(cl)
			}
			if c.RemoteHost() && cl != nil && cl.Connected() && cl != s.updateClient {
				s.connectUpdate(cl)
			}
		})
	})
	return s
}
func (s *settingsService) changed() { s.revision.Update(func(n int) int { return n + 1 }) }
func (s *settingsService) live(get func() bool) func() bool {
	return Live(func() bool { s.revision.Get(); return get() })
}
func (s *settingsService) failRestart(err error) {
	s.phase = "failed"
	s.restartError = err.Error()
	s.changed()
}
func (s *settingsService) checkRestart() {
	cl := s.c.State().Remote
	if cl == nil || !cl.Connected() {
		return
	}
	s.restartClient = cl
	s.phase = "checking"
	s.restartError = ""
	s.changed()
	go func() {
		var h svcproto.Hello
		err := cl.Call("hello", nil, &h)
		s.cx.Post(func() {
			if s.restartClient != cl || s.phase != "checking" {
				return
			}
			if s.c.State().Remote != cl {
				err = svcproto.ErrClosed
			}
			if err != nil {
				s.failRestart(err)
				return
			}
			s.restartPID = h.PID
			if h.Busy {
				s.phase, s.back = "confirm", "idle"
				s.changed()
				return
			}
			s.phase = "idle"
			s.sendRestart(true)
		})
	}()
}
func (s *settingsService) sendRestart(idle bool) {
	cl := s.restartClient
	if cl == nil || cl != s.c.State().Remote || !cl.Connected() {
		s.failRestart(svcproto.ErrClosed)
		return
	}
	s.phase, s.whenIdle = "requesting", idle
	s.changed()
	op := "quit"
	if idle {
		op = "quitWhenIdle"
	}
	go func() {
		err := cl.Call(op, nil, nil)
		s.cx.Post(func() {
			if s.restartClient != cl || s.phase != "requesting" {
				return
			}
			if err != nil && cl.Connected() {
				s.failRestart(err)
				return
			}
			s.phase = "waiting"
			s.changed()
			if next := s.c.State().Remote; next != nil && next != cl && next.Connected() {
				s.verifyRestart(next)
			}
		})
	}()
}
func (s *settingsService) verifyRestart(cl *svcclient.Client) {
	s.phase = "verifying"
	s.changed()
	go func() {
		var h svcproto.Hello
		err := cl.Call("hello", nil, &h)
		s.cx.Post(func() {
			if s.phase != "verifying" {
				return
			}
			if s.c.State().Remote != cl {
				s.phase = "waiting"
				s.changed()
				return
			}
			if err != nil {
				s.failRestart(err)
				return
			}
			if h.PID <= 0 || h.PID == s.restartPID {
				s.failRestart(fmt.Errorf("%s", tr("The background service has not exited. Try restarting again.")))
				return
			}
			s.phase = "done"
			s.changed()
		})
	}()
}
func (s *settingsService) connectUpdate(cl *svcclient.Client) {
	s.updateClient = cl
	s.updateBusy = true
	s.updateError = ""
	s.changed()
	go func() {
		var h svcproto.Hello
		err := cl.Call("hello", nil, &h)
		s.cx.Post(func() {
			if s.c.State().Remote != cl || s.updateClient != cl {
				return
			}
			s.updateBusy = false
			if err != nil {
				s.updateError = err.Error()
				s.changed()
				return
			}
			if s.wanted != "" && h.Release == s.wanted && h.PID > 0 && h.PID != s.updatePID {
				s.update = selfupdate.Status{Supported: h.SelfUpdate, Current: h.Release, Phase: "done"}
				s.wanted = ""
				s.changed()
				return
			}
			if s.wanted != "" {
				s.updateError = tr("The target has reconnected, but the requested Miya version is not running yet.")
			}
			s.updatePID = h.PID
			s.update = selfupdate.Status{Current: h.Release, Supported: h.SelfUpdate, Phase: "unsupported"}
			s.changed()
			if h.SelfUpdate {
				s.callUpdate("status")
			}
		})
	}()
}
func (s *settingsService) callUpdate(action string) {
	cl := s.updateClient
	if s.updateBusy || cl == nil || cl != s.c.State().Remote || !cl.Connected() {
		return
	}
	version := s.update.Latest
	if action == "install" {
		s.wanted = version
	}
	s.updateBusy = true
	if action != "status" {
		s.updateError = ""
	}
	s.changed()
	go func() {
		var status selfupdate.Status
		err := cl.Call("miyaUpdate", struct{ Action, Version string }{action, version}, &status)
		s.cx.Post(func() {
			if s.updateClient != cl || s.c.State().Remote != cl {
				return
			}
			s.updateBusy = false
			if err != nil {
				s.updateError = err.Error()
				s.changed()
				return
			}
			s.update = status
			if s.wanted == "" && (status.Phase == "installing" || status.Phase == "waiting") {
				s.wanted = status.Latest
			}
			if status.Phase == "failed" {
				s.wanted = ""
			}
			s.changed()
			if action == "status" && status.Phase == "idle" && s.wanted == "" && status.Supported {
				s.callUpdate("check")
				return
			}
			switch status.Phase {
			case "checking", "installing", "waiting":
				s.cx.AfterFunc(time.Second, func() { s.callUpdate("status") })
			}
		})
	}()
}
func (s *settingsService) groups() []ui.SettingsGroup {
	restart := core.Dynamic(func(cx *core.Scope) core.Node {
		s.revision.Get()
		Live(func() bool { return s.c.State().Remote != nil })()
		help := tr("The window reconnects automatically and keeps unsent drafts.")
		if s.c.State().Remote == nil {
			help = tr("Standalone mode has no separate background service.")
		}
		label := tr("Restart")
		if s.phase == "checking" || s.phase == "requesting" || s.phase == "verifying" {
			label = tr("Working…")
		}
		nodes := []core.Node{settingsText(cx, func() string { return help }), settingsButton("restart-server", label, s.checkRestart, s.live(func() bool {
			cl := s.c.State().Remote
			return cl == nil || !cl.Connected() || s.phase != "idle" && s.phase != "done" && s.phase != "failed"
		}))}
		text := ""
		switch s.phase {
		case "confirm":
			text = tr("Background tasks are still running. Restarting now interrupts tasks across all projects and briefly disconnects phones.")
			nodes = append(nodes, settingsActions(settingsButton("cancel-restart", tr("Cancel"), func() { s.phase = s.back; s.changed() }, nil), settingsButton("restart-idle", tr("Restart when idle"), func() { s.sendRestart(true) }, nil), settingsButton("restart-now", tr("Restart now"), func() { s.sendRestart(false) }, nil)))
		case "failed":
			text = tr("Restart failed: ") + s.restartError
		case "requesting", "waiting", "verifying":
			text = tr("Restarting the background service; waiting to reconnect…")
			if s.whenIdle {
				text = tr("Restart scheduled. The window will reconnect after background tasks finish.")
			}
			if s.phase == "waiting" && s.whenIdle && s.restartClient.Connected() {
				nodes = append(nodes, settingsButton("override-restart", tr("Restart now…"), func() { s.phase, s.back = "confirm", "waiting"; s.changed() }, nil))
			}
		case "done":
			text = tr("Background service restarted and reconnected.")
		}
		nodes = append(nodes, settingsText(cx, func() string { return text }))
		return settingsColumn(nodes...)
	})
	groups := []ui.SettingsGroup{{ID: "service", Title: tr("Background service"), Items: []ui.SettingsItem{{ID: "restart-server", Title: tr("Restart background service"), Content: restart}}}}
	// The category remains stable across workspace connections; the region hides
	// remote-only controls when the controller is operating on this computer.
	groups = append(groups, ui.SettingsGroup{ID: "miya-update", Items: []ui.SettingsItem{{ID: "miya-update", Title: tr("Miya on this workspace"), Content: core.Show{When: Live(s.c.RemoteHost), Then: func(cx *core.Scope) core.Node {
		return core.Dynamic(func(cx *core.Scope) core.Node {
			s.revision.Get()
			status := s.update
			messages := []string{tr("Miya on this workspace"), fmt.Sprintf(tr("Running version: %s"), status.Current)}
			switch status.Phase {
			case "unsupported":
				messages = append(messages, tr("Install an updated Miya on the target computer once to enable remote upgrades."))
			case "available":
				messages = append(messages, fmt.Sprintf(tr("Available version: %s"), status.Latest), tr("The target downloads and verifies the release, keeps a backup, and restarts after running tasks finish."))
			case "checking":
				messages = append(messages, tr("Checking for Miya updates…"))
			case "installing":
				messages = append(messages, tr("Downloading and verifying Miya on the target…"))
			case "waiting":
				messages = append(messages, tr("Update installed. Waiting for tasks to finish and the new background service to reconnect…"))
			case "installed":
				messages = append(messages, tr("Update installed. Reopen Miya on the target computer to finish switching versions."))
			case "done":
				messages = append(messages, tr("The target is running the requested Miya version."))
			case "current":
				messages = append(messages, tr("Miya is up to date on this workspace."))
			}
			if status.Backup != "" {
				messages = append(messages, fmt.Sprintf(tr("Backup on target: %s"), status.Backup))
			}
			messages = append(messages, status.Error, s.updateError)
			var nodes []core.Node
			for _, msg := range messages {
				nodes = append(nodes, settingsText(cx, func() string { return msg }))
			}
			if status.Phase == "available" {
				nodes = append(nodes, settingsButton("install-miya-update", tr("Upgrade target"), func() { s.callUpdate("install") }, s.live(func() bool { return s.updateBusy })))
			}
			nodes = append(nodes, settingsButton("check-miya-update", tr("Check for Miya updates"), func() { s.callUpdate("check") }, s.live(func() bool {
				return s.updateBusy || !s.update.Supported || s.update.Phase == "checking" || s.update.Phase == "installing" || s.update.Phase == "waiting" || s.update.Phase == "installed"
			})))
			return settingsColumn(nodes...)
		})
	}}}}})
	return groups
}
