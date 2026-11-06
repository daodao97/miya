package controller

import (
	"context"

	"miya/internal/async"
	"miya/internal/automation"
	mcpconfig "miya/internal/mcp/config"
	"miya/internal/remote"
	"miya/internal/service/process"
	serviceserver "miya/internal/service/server"
	"miya/internal/uiruntime"
)

func runService() error { return runServiceWithOptions(serverOptions{}) }
func runServiceWithOptions(options serverOptions) error {
	servingRemote = options.Persistent
	return serviceserver.Run(func(ex *async.Executor) *serviceserver.Service {
		uiruntime.RunOnMain = ex.Push
		mcpconfig.Enabled = true
		c := newChatController()
		s := NewService(c, ex)
		s.Persistent = options.Persistent
		c.StartAutomations(automation.Path(), true)
		exe, at := process.ExecutableInfo()
		s.SetExecutable(exe, at)
		uiruntime.SetInvalidateHook(s.MarkDirty)
		DesktopPhone = NewPhoneHost(c)
		if options.Persistent {
			DesktopPhone.Service = func(ctx context.Context, conn *remote.Conn) { s.HandleConn(&serviceserver.Peer{Wire: conn}) }
			DesktopPhone.Terminal = c.ServeRemoteTerminal
			DesktopPhone.SaveLink = func(link string) (string, error) { return ConnectionFile(), SaveServerConnection(link) }
			DesktopPhone.Start()
		}
		return s
	}, uiruntime.RunCleanups)
}
