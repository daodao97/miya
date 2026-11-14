package controller

import (
	"context"

	"miya/internal/async"
	"miya/internal/automation"
	mcpconfig "miya/internal/mcp/config"
	"miya/internal/remote"
	"miya/internal/selfupdate"
	"miya/internal/service/process"
	svcproto "miya/internal/service/protocol"
	serviceserver "miya/internal/service/server"
	"miya/internal/uiruntime"
)

func runService() error { return runServiceWithOptions(serverOptions{}) }
func runServiceWithOptions(options serverOptions) error {
	servingRemote = options.Persistent
	var running *serviceserver.Service
	exe, at := process.ExecutableInfo()
	err := serviceserver.Run(func(ex *async.Executor) *serviceserver.Service {
		uiruntime.RunOnMain = ex.Push
		mcpconfig.Enabled = true
		c := newChatController()
		s := NewService(c, ex)
		running = s
		s.Persistent = options.Persistent
		s.Updates = selfupdate.New(exe, options.Persistent, svcproto.Version)
		c.StartAutomations(automation.Path(), true)
		s.SetExecutable(exe, at)
		uiruntime.SetInvalidateHook(s.MarkDirty)
		DesktopPhone = NewPhoneHost(c, func(p *phoneHost) {
			p.Service = func(ctx context.Context, conn *remote.Conn) { s.HandleConn(&serviceserver.Peer{Wire: conn}) }
			p.Terminal = c.ServeRemoteTerminal
		})
		if options.Persistent {
			DesktopPhone.SaveLink = func(link string) (string, error) { return ConnectionFile(), SaveServerConnection(link) }
			DesktopPhone.Start()
		}
		return s
	}, uiruntime.RunCleanups)
	if err == nil && running != nil && running.Updates.Status().Phase == "waiting" && !process.Supervised() {
		// systemd supervises -serve itself. An ordinary desktop backend or a
		// foreground server must keep the remote endpoint available without a
		// connected window. Run only after cleanup and release of the old lock.
		return process.SpawnReplacement(exe)
	}
	return err
}
