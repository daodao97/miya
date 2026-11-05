package main

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
)

type mobileDesktopPrivacy struct {
	available, started            bool
	enabled, known, busy, polling bool
	revision                      int
	message                       string
}

func (m *mobileApp) desktopInitPrivacy(p *mobileDesktopPage, s desktopSession) {
	p.privacy.available = s.Pick && s.Privacy
	if p.privacy.available && !p.privacy.started {
		p.privacy.started = true
		m.desktopPollPrivacy(p)
	}
}

func (m *mobileApp) desktopPollPrivacy(p *mobileDesktopPage) {
	if !p.privacy.available || p.privacy.polling {
		return
	}
	if p.privacy.busy {
		m.desktopAfter(p, time.Second, func() { m.desktopPollPrivacy(p) })
		return
	}
	p.privacy.polling = true
	revision := p.privacy.revision
	m.desktopDo(p, func(ctx context.Context) func() {
		state, err := p.client.privacy(ctx, nil)
		return func() {
			p.privacy.polling = false
			if revision == p.privacy.revision {
				if desktopStatus(err) == 410 {
					m.desktopFinish(p, err.Error())
					return
				}
				if err == nil {
					p.privacy.enabled, p.privacy.known, p.privacy.message = state.Enabled, true, ""
				} else {
					p.privacy.known, p.privacy.message = false, tr("Privacy mode status is unconfirmed. Check the connection.")
				}
			}
			m.desktopAfter(p, 2*time.Second, func() { m.desktopPollPrivacy(p) })
		}
	})
}

func (m *mobileApp) desktopSetPrivacy(p *mobileDesktopPage) {
	if !p.pickable || !p.privacy.available || p.privacy.busy {
		return
	}
	// Even with an uncertain state, offering disable lets the person restore
	// the Mac. Enabling requires a fresh, visible desktop and known state.
	enabled := !p.privacy.enabled && p.privacy.known
	if enabled && (!p.active || p.image == nil || p.unlock.locked || p.unlock.busy) {
		return
	}
	p.privacy.busy, p.privacy.message = true, ""
	p.privacy.revision++
	m.desktopDo(p, func(ctx context.Context) func() {
		state, err := p.client.privacy(ctx, &enabled)
		return func() {
			p.privacy.busy = false
			if desktopStatus(err) == 410 {
				m.desktopFinish(p, err.Error())
				return
			}
			if err != nil {
				p.privacy.known = false
				p.privacy.message = tr("Unable to change privacy mode") + ": " + err.Error()
				return
			}
			p.privacy.enabled, p.privacy.known = state.Enabled, true
		}
	})
}

func (m *mobileApp) desktopPrivacyView(c *ui.Context, p *mobileDesktopPage) {
	if !p.pickable || !p.privacy.available {
		return
	}
	pal := m.pal
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			icon(c, "eye", 18, pal.ink)
			ui.Text(c, tr("Privacy mode")).FontSize(m.font(15)).FontWeight(600).Grow(1).MinWidth(0)
			status := tr("Off")
			if p.privacy.enabled {
				status = tr("On")
			}
			if !p.privacy.known {
				status = tr("Checking…")
			}
			ui.Text(c, status).FontSize(m.font(13)).TextColor(pal.muted)
		})
		ui.Text(c, tr("Black out your Mac displays while you keep working here. Press Control–Option–Command–P on the Mac to restore them.")).FontSize(m.font(13)).TextColor(pal.muted).LineHeight(1.4)
		if p.privacy.message != "" {
			ui.Text(c, p.privacy.message).FontSize(m.font(13)).TextColor(pal.warn).LineHeight(1.4)
		}
		label := tr("Enable privacy mode")
		if p.privacy.enabled || !p.privacy.known {
			label = tr("Restore Mac displays")
		}
		disabled := p.privacy.busy || (!p.privacy.enabled && p.privacy.known && (!p.active || p.image == nil || p.unlock.locked || p.unlock.busy))
		if m.pill(c, "desktop-privacy-toggle", label, !p.privacy.enabled).FillWidth().Disabled(disabled).Clicked() {
			m.desktopSetPrivacy(p)
		}
	})
}
