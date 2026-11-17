//go:build ios || android

package desktopui

import "github.com/egoist/mygo/ui"

// panelTerminal stands for the desktop panel's terminal on phones, which
// have no libghostty-vt for plugins/terminal: the panel says it is
// unavailable.
type panelTerminal struct{}

func (*panelTerminal) Close() error  { return nil }
func (*panelTerminal) ExitCode() int { return 0 }

func (app *App) startTerminal(s *PanelSurface, dir string) {
	s.Starting, s.termErr = false, tr("Terminal unavailable")
}

func terminalExited(*panelTerminal) bool { return true }

func panelTerminalView(*ui.Context, *panelTerminal) {}
