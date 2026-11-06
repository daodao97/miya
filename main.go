package main

import (
	"runtime"

	"miya/internal/agent/integrations"
	"miya/internal/controller"
	"miya/internal/mobile"
	desktopui "miya/internal/ui"
)

func main() {
	if runtime.GOOS == "ios" {
		mobile.MobileEntry()
		return
	}
	if controller.RunCommands() {
		return
	}
	desktopui.DesktopMain()
}

func init() {
	desktopui.ConfigureEnvironment(desktopui.Environment{
		Quotas:          integrations.Quotas,
		NewWindow:       func() desktopui.Host { return controller.NewWindowController() },
		NewLocal:        func() desktopui.Host { return controller.NewLocalWindowController() },
		NewMirror:       func() desktopui.Host { return controller.NewMirrorController() },
		LocalConnection: controller.LocalWindowConnection,
	})
}
