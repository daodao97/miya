package main

import (
	controller "miya/internal/controller"
	mobile "miya/internal/mobile"
	desktopui "miya/internal/ui"
	runtime "runtime"
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
		NewWindow:       func() desktopui.Host { return controller.NewWindowController() },
		NewLocal:        func() desktopui.Host { return controller.NewLocalWindowController() },
		NewMirror:       func() desktopui.Host { return controller.NewMirrorController() },
		LocalConnection: controller.LocalWindowConnection,
	})
}
