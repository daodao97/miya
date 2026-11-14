//go:build nsgo && !ios && !android

// The desktop app on NSGo, built with -tags nsgo while the migration runs;
// see docs/nsgo-migration.md. Phones keep the MyGo entry in main.go.
package main

import (
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
		NewWindow: func() nsui.Host { return controller.NewWindowController() },
	})
}
