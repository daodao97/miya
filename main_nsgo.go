//go:build nsgo && !ios && !android

// The desktop app on NSGo, built with -tags nsgo while the migration runs;
// see docs/nsgo-migration.md. Phones keep the MyGo entry in main.go.
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
