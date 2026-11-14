package nsmobile

import (
	"errors"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/mobile"
	"miya/internal/remote"
)

func (m *App) pair(raw string) {
	raw = strings.TrimSpace(raw)
	if _, err := remote.ParseLink(raw); err != nil {
		m.Error = err.Error()
		return
	}
	m.Connect(raw)
	m.sheet.Set("")
	m.pairCode.Set("")
}

func (m *App) scan() {
	if m.scanning {
		return
	}
	m.scanning, m.Error = true, ""
	// Native completion may outlive the pairing sheet. It cannot replace a
	// desktop selected in the meantime or pair after the app backgrounds.
	generation, sheet := m.Generation, m.sheet.Get()
	mobile.ScanQRCode(m.cx, func(code string, ok bool, err error) {
		m.scanning = false
		if generation != m.Generation || sheet != m.sheet.Get() || m.cx.Env().Lifecycle() == core.LifecycleBackground {
			m.change()
			return
		}
		if err != nil {
			if errors.Is(err, core.ErrPlatformUnsupported) {
				m.Error = tr("Scan on your phone, or paste a desktop connection code")
			} else {
				m.Error = tr("Unable to open the camera")
			}
		} else if ok {
			m.pair(code)
		}
		m.change()
	})
}
