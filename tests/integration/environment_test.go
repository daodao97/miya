package integration

import (
	"os"
	"path/filepath"
	"runtime"

	"miya/internal/agent/integrations"
	"miya/internal/controller"
	desktopui "miya/internal/ui"
)

// Keep fixture paths rooted in the checkout even for a separately launched QA binary.
func init() {
	// Helper processes intentionally run in a temporary project. Discovery
	// identifies their listeners by that cwd, so preserve the parent's choice.
	if os.Getenv("MIYA_WEB_DISCOVERY_HELPER") != "" || os.Getenv("MIYA_COLLABORATION_MCP_HELPER") != "" {
		return
	}
	_, source, _, ok := runtime.Caller(0)
	if ok && filepath.IsAbs(source) {
		if err := os.Chdir(filepath.Join(filepath.Dir(source), "../..")); err != nil {
			panic(err)
		}
	} else {
		dir, err := os.Getwd()
		if err != nil {
			panic(err)
		}
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				panic("Miya checkout not found")
			}
			dir = parent
		}
		if err := os.Chdir(dir); err != nil {
			panic(err)
		}
	}
	desktopui.ConfigureEnvironment(desktopui.Environment{
		Quotas:          integrations.Quotas,
		NewWindow:       func() desktopui.Host { return controller.NewWindowController() },
		NewLocal:        func() desktopui.Host { return controller.NewLocalWindowController() },
		NewMirror:       func() desktopui.Host { return controller.NewMirrorController() },
		LocalConnection: controller.LocalWindowConnection,
	})
}
