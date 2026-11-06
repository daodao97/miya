package mobile

import (
	context "context"
	control "miya/internal/desktop/control"
	widgets "miya/internal/widgets"
	time "time"

	ui "github.com/egoist/mygo/ui"
)

type mobileDesktopPrivacy struct {
	Available, started            bool
	Enabled, Known, Busy, polling bool
	revision                      int
	message                       string
}

func (m *App) DesktopInitPrivacy(p *MobileDesktopPage, s control.Session) {
	p.Privacy.Available = s.Pick && s.Privacy
	if p.Privacy.Available && !p.Privacy.started {
		p.Privacy.started = true
		m.desktopPollPrivacy(p)
	}
}

func (m *App) desktopPollPrivacy(p *MobileDesktopPage) {
	if !p.Privacy.Available || p.Privacy.polling {
		return
	}
	if p.Privacy.Busy {
		m.desktopAfter(p, time.Second, func() { m.desktopPollPrivacy(p) })
		return
	}
	p.Privacy.polling = true
	revision := p.Privacy.revision
	m.desktopDo(p, func(ctx context.Context) func() {
		state, err := p.Client.Privacy(ctx, nil)
		return func() {
			p.Privacy.polling = false
			if revision == p.Privacy.revision {
				if control.Status(err) == 410 {
					m.DesktopFinish(p, err.Error())
					return
				}
				if err == nil {
					p.Privacy.Enabled, p.Privacy.Known, p.Privacy.message = state.Enabled, true, ""
				} else {
					p.Privacy.Known, p.Privacy.message = false, tr("Privacy mode status is unconfirmed. Check the connection.")
				}
			}
			m.desktopAfter(p, 2*time.Second, func() { m.desktopPollPrivacy(p) })
		}
	})
}

func (m *App) DesktopSetPrivacy(p *MobileDesktopPage) {
	if !p.Pickable || !p.Privacy.Available || p.Privacy.Busy {
		return
	}
	// Even with an uncertain state, offering disable lets the person restore
	// the Mac. Enabling requires a fresh, visible desktop and known state.
	enabled := !p.Privacy.Enabled && p.Privacy.Known
	if enabled && (!p.Active || p.Image == nil || p.Unlock.Locked || p.Unlock.Busy) {
		return
	}
	p.Privacy.Busy, p.Privacy.message = true, ""
	p.Privacy.revision++
	m.desktopDo(p, func(ctx context.Context) func() {
		state, err := p.Client.Privacy(ctx, &enabled)
		return func() {
			p.Privacy.Busy = false
			if control.Status(err) == 410 {
				m.DesktopFinish(p, err.Error())
				return
			}
			if err != nil {
				p.Privacy.Known = false
				p.Privacy.message = tr("Unable to change privacy mode") + ": " + err.Error()
				return
			}
			p.Privacy.Enabled, p.Privacy.Known = state.Enabled, true
		}
	})
}

func (m *App) desktopPrivacyView(c *ui.Context, p *MobileDesktopPage) {
	if !p.Pickable || !p.Privacy.Available {
		return
	}
	pal := m.Pal
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			widgets.Icon(c, "eye", 18, pal.ink)
			ui.Text(c, tr("Privacy mode")).FontSize(m.Font(15)).FontWeight(600).Grow(1).MinWidth(0)
			status := tr("Off")
			if p.Privacy.Enabled {
				status = tr("On")
			}
			if !p.Privacy.Known {
				status = tr("Checking…")
			}
			ui.Text(c, status).FontSize(m.Font(13)).TextColor(pal.muted)
		})
		ui.Text(c, tr("Black out your Mac displays while you keep working here. Press Control–Option–Command–P on the Mac to restore them.")).FontSize(m.Font(13)).TextColor(pal.muted).LineHeight(1.4)
		if p.Privacy.message != "" {
			ui.Text(c, p.Privacy.message).FontSize(m.Font(13)).TextColor(pal.warn).LineHeight(1.4)
		}
		label := tr("Enable privacy mode")
		if p.Privacy.Enabled || !p.Privacy.Known {
			label = tr("Restore Mac displays")
		}
		disabled := p.Privacy.Busy || (!p.Privacy.Enabled && p.Privacy.Known && (!p.Active || p.Image == nil || p.Unlock.Locked || p.Unlock.Busy))
		if m.pill(c, "desktop-privacy-toggle", label, !p.Privacy.Enabled).FillWidth().Disabled(disabled).Clicked() {
			m.DesktopSetPrivacy(p)
		}
	})
}
