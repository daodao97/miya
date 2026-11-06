package desktopui

import (
	"os"
	"runtime"
)

// Carbon delivers the recovery hotkey on the startup thread. Bind it during
// init, before main can migrate to another OS thread. Other Miya modes keep
// their existing threading behavior.
func init() {
	if runtime.GOOS == "darwin" && len(os.Args) == 2 && os.Args[1] == "-desktop-privacy" {
		runtime.LockOSThread()
	}
}
