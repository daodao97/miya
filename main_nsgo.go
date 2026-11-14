//go:build !ios && !android

// The desktop app entry. Phones use main_nsgo_mobile.go.
package main

import (
	"miya/internal/agent/integrations"
	"miya/internal/controller"
	"miya/internal/nsui"
)

func main() {
	if controller.RunCommands() {
		return
	}
	nsui.DesktopMain()
}

func init() {
	nsui.ConfigureEnvironment(nsui.Environment{
		Quotas:          integrations.Quotas,
		NewWindow:       func() nsui.Host { return controller.NewWindowController() },
		NewLocal:        func() nsui.Host { return controller.NewLocalWindowController() },
		NewMirror:       func() nsui.MirrorHost { return controller.NewMirrorController() },
		LocalConnection: controller.LocalWindowConnection,
	})
}
