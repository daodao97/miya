//go:build !ios && !android

package desktopui

import (
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"miya/internal/uiruntime"
)

// panelTerminal is the panel's terminal, on desktops: phones have no
// libghostty-vt (panel_terminal_phone.go).
type panelTerminal = terminal.Terminal

func panelTerminalView(c *ui.Context, t *panelTerminal) {
	terminal.View(c, t).Fill().AutoFocus()
}

func (app *App) startTerminal(s *PanelSurface, dir string) {
	s.Starting, s.termErr = true, ""
	files := app.C.WorkspaceFiles()
	uiruntime.GoAsync(func() {
		opts := terminal.Options{
			Dir:                     dir,
			ShellIntegration:        true,
			CopyOnSelect:            terminal.CopyOnSelectClipboard,
			SelectOnDrag:            true,
			SelectOnDragInAltScreen: true,
		}
		var err error
		opts.Conn, err = files.Terminal(dir)
		var t *terminal.Terminal
		if err == nil {
			t, err = terminal.New(opts)
		}
		if err != nil && opts.Conn != nil {
			opts.Conn.Close()
		}
		app.C.Dispatch(func() {
			s.Starting = false
			if err != nil {
				s.termErr = err.Error()
				return
			}
			if s.closed {
				go t.Close()
				return
			}
			s.Term = t
			go func() {
				<-t.Done()
				uiruntime.Invalidate()
			}()
		})
	})
}

func terminalExited(t *terminal.Terminal) bool {
	select {
	case <-t.Done():
		return true
	default:
		return false
	}
}
