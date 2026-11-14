package client

import (
	"context"
	"time"

	"miya/internal/desktop/control"
)

type mobileDesktopPrivacy struct {
	Available, started            bool
	Enabled, Known, Busy, polling bool
	revision                      int
	Message                       string
}

func (m *Client) DesktopInitPrivacy(p *MobileDesktopPage, s control.Session) {
	p.Privacy.Available = s.Pick && s.Privacy
	if p.Privacy.Available && !p.Privacy.started {
		p.Privacy.started = true
		m.desktopPollPrivacy(p)
	}
}

func (m *Client) desktopPollPrivacy(p *MobileDesktopPage) {
	if !p.Privacy.Available || p.Privacy.polling {
		return
	}
	if p.Privacy.Busy {
		m.desktopAfter(p, time.Second, func() { m.desktopPollPrivacy(p) })
		return
	}
	p.Privacy.polling = true
	revision := p.Privacy.revision
	m.desktopLeaseDo(p, func(ctx context.Context) func() {
		state, err := p.Client.Privacy(ctx, nil)
		return func() {
			p.Privacy.polling = false
			if revision == p.Privacy.revision {
				if control.Status(err) == 410 {
					m.DesktopFinish(p, err.Error())
					return
				}
				if err == nil {
					p.Privacy.Enabled, p.Privacy.Known, p.Privacy.Message = state.Enabled, true, ""
				} else {
					p.Privacy.Known, p.Privacy.Message = false, tr("Privacy mode status is unconfirmed. Check the connection.")
				}
			}
			m.desktopAfter(p, 2*time.Second, func() { m.desktopPollPrivacy(p) })
		}
	})
}

func (m *Client) DesktopSetPrivacy(p *MobileDesktopPage) {
	if !p.Pickable || !p.Privacy.Available || p.Privacy.Busy {
		return
	}
	// Even with an uncertain state, offering disable lets the person restore
	// the Mac. Enabling requires a fresh, visible desktop and known state.
	enabled := !p.Privacy.Enabled && p.Privacy.Known
	if enabled && (!p.Active || p.Image == nil || p.Unlock.Locked || p.Unlock.Busy) {
		return
	}
	p.Privacy.Busy, p.Privacy.Message = true, ""
	p.Privacy.revision++
	m.desktopLeaseDo(p, func(ctx context.Context) func() {
		state, err := p.Client.Privacy(ctx, &enabled)
		return func() {
			p.Privacy.Busy = false
			if control.Status(err) == 410 {
				m.DesktopFinish(p, err.Error())
				return
			}
			if err != nil {
				p.Privacy.Known = false
				p.Privacy.Message = tr("Unable to change privacy mode") + ": " + err.Error()
				return
			}
			p.Privacy.Enabled, p.Privacy.Known = state.Enabled, true
		}
	})
}

// Restore is always an explicit disable, including when the last observed
// state was unknown. It must never invert into an enable on a stale button.
func (m *Client) DesktopRestorePrivacy(p *MobileDesktopPage) {
	if !p.Pickable || !p.Privacy.Available || p.Privacy.Busy {
		return
	}
	p.Privacy.Busy = true
	p.Privacy.revision++
	m.desktopLeaseDo(p, func(ctx context.Context) func() {
		enabled := false
		state, err := p.Client.Privacy(ctx, &enabled)
		return func() {
			p.Privacy.Busy = false
			if control.Status(err) == 410 {
				m.DesktopFinish(p, err.Error())
				return
			}
			if err != nil {
				p.Privacy.Known = false
				p.Privacy.Message = err.Error()
				return
			}
			p.Privacy.Enabled, p.Privacy.Known, p.Privacy.Message = state.Enabled, true, ""
		}
	})
}
