package integration

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"miya/internal/agent/integrations"
	"miya/internal/controller"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
	desktopui "miya/internal/ui"
)

// isolated refuses the installed app's background service. Without a data
// directory of its own, a window factory would connect to it, and from a test
// binary it would replace that service or ask it to quit once idle.
func isolated() error {
	if os.Getenv("MIYA_DIR") != "" || os.Getenv("MIYA_ISOLATED_DIR") != "" {
		return nil
	}
	return errors.New("integration test reached the installed Miya background service; set MIYA_DIR")
}

func refusedWindow(err error) desktopui.Host {
	return controller.NewServiceWindow("", func() (*svcclient.Client, svcproto.State, error) { return nil, svcproto.State{}, err })
}

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
		Quotas: integrations.Quotas,
		NewWindow: func() desktopui.Host {
			if err := isolated(); err != nil {
				return refusedWindow(err)
			}
			return controller.NewWindowController()
		},
		NewLocal: func() desktopui.Host {
			if err := isolated(); err != nil {
				return refusedWindow(err)
			}
			return controller.NewLocalWindowController()
		},
		NewMirror: func() desktopui.Host { return controller.NewMirrorController() },
		LocalConnection: func() (*svcclient.Client, svcproto.State, error) {
			if err := isolated(); err != nil {
				return nil, svcproto.State{}, err
			}
			return controller.LocalWindowConnection()
		},
	})
}
