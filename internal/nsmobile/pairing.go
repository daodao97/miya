package nsmobile

import (
	"errors"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"miya/internal/remote"
)

func (m *App) pair(raw string) {
	raw = strings.TrimSpace(raw)
	if _, err := remote.ParseLink(raw); err != nil {
		m.Error = err.Error()
		return
	}
	m.Connect(raw)
	m.pages.Open("")
	m.pairCode.Set("")
}

func (m *App) scan() {
	if m.scanning {
		return
	}
	m.scanning, m.Error = true, ""
	// Native completion may outlive the pairing sheet. It cannot replace a
	// desktop selected in the meantime or pair after the app backgrounds.
	generation, sheet := m.Generation, m.pages.Top()
	m.cx.ScanCode(core.ScanOptions{}, func(code *core.ScannedCode, err error) {
		m.scanning = false
		if generation != m.Generation || sheet != m.pages.Top() || m.cx.Env().Lifecycle() == core.LifecycleBackground {
			m.change()
			return
		}
		if err != nil {
			if errors.Is(err, core.ErrNoScanner) {
				m.Error = tr("Scan on your phone, or paste a desktop connection code")
			} else {
				m.Error = tr("Unable to open the camera")
			}
		} else if code != nil {
			m.pair(code.Text)
		}
		m.change()
	})
}
