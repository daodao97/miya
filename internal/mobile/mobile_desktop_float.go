package mobile

import (
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/textutil"
	"miya/internal/uiruntime"
	"miya/internal/widgets"
)

// desktopFloat is the in-app floating window of a minimized remote desktop.
// The lease stays open and the screen keeps refreshing, but no touch on the
// floating window reaches the Mac: tapping it restores the full page.
type desktopFloat struct {
	handle                       ui.Handle
	placed                       bool
	x, y                         float32
	pressing                     bool
	dragged                      bool
	startX, startY, fromX, fromY float32
}

// DesktopFloatPollInterval slows the screen refresh while minimized.
const DesktopFloatPollInterval = time.Second

// DesktopMinimize leaves the remote desktop page for the previous one and
// keeps the session in a floating window.
func (m *App) DesktopMinimize(p *MobileDesktopPage) {
	if p == nil || m.DesktopPage != p || p.Phase == "ended" {
		return
	}
	p.Floating = true
	p.keyboard, p.More, p.moreKeys, p.focusText = false, false, false, false
	p.pointer, p.text, p.modifier = desktopPointer{}, "", ""
	p.Queue = nil
	if m.Router().Path() == "/desktop" {
		m.Router().Back()
	}
}

// DesktopRestore brings a floating session back to its full page.
func (m *App) DesktopRestore(p *MobileDesktopPage) {
	if p == nil || m.DesktopPage != p || !p.Floating {
		return
	}
	p.Floating = false
	if m.Router().Path() != "/desktop" {
		m.Router().Push("/desktop")
	}
	// The existing frame poll keeps running and uses the full-page cadence
	// after its next response. Starting another here duplicates that chain.
}

// desktopFloatSize keeps the frame's aspect inside a small window.
func desktopFloatSize(p *MobileDesktopPage, rootW float32) (float32, float32) {
	w := desktopClamp(rootW*0.42, 120, 220)
	aspect := float32(1.6)
	if p.Image != nil {
		if iw, ih := ui.ImageSize(p.Image); iw > 0 && ih > 0 {
			aspect = desktopClamp(iw/ih, 0.6, 2.4)
		}
	}
	return w, w / aspect
}

// desktopFloatView draws the floating window above every page.
func (m *App) desktopFloatView(c *ui.Context) {
	p := m.DesktopPage
	if p == nil || !p.Floating || m.Router().Path() == "/desktop" {
		return
	}
	root, safe, pal := c.Root().Bounds(), c.SafeArea(), m.Pal
	if root.W <= 0 || root.H <= 0 {
		return
	}
	w, h := desktopFloatSize(p, root.W)
	f := &p.float
	const margin = 12
	minX, maxX := safe.Left+margin, root.W-safe.Right-margin-w
	minY, maxY := safe.Top+margin, root.H-safe.Bottom-margin-h
	if !f.placed {
		f.x, f.y, f.placed = maxX, maxY-96, true
	}
	if !f.pressing {
		f.x, f.y = desktopClamp(f.x, minX, max(minX, maxX)), desktopClamp(f.y, minY, max(minY, maxY))
	}
	ended := p.Phase == "ended"
	ui.Overlay(c, func() {
		label := tr("Remote desktop floating window. Tap to return; drag to move.")
		if ended {
			label = tr("Control session ended. Tap to close.")
		}
		card := ui.Column(c.Key("desktop-float")).Absolute().Left(f.x).Top(f.y).Width(w).Height(h).Radius(12).Clip().
			Background(pal.surface).Border(1, pal.line).Label(label).Bind(&f.handle)
		// A large blurred shadow repaints a wide area each frame; skip it
		// while dragging so the move stays smooth.
		if !f.dragged || !f.pressing {
			card.Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.3))
		}
		card.HandleInput(func(ev ui.InputEvent) bool {
			// Events are local to the card's latest layout, which moves during
			// a drag. Convert using its actual bounds, not the pending position.
			r := f.handle.Bounds(c)
			ev.X, ev.Y = ev.X+r.X, ev.Y+r.Y
			return m.desktopFloatInput(p, ev, minX, maxX, minY, maxY)
		})
		card.Children(func() {
			if p.Image != nil && !ended {
				ui.Image(c, p.Image).Fill().Fit(ui.Contain)
				return
			}
			ui.Column(c).Fill().Padding(8).Gap(6).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				switch {
				case ended:
					widgets.Icon(c, "device-desktop", 22, pal.muted)
					ui.Text(c, tr("Control session ended")).FontSize(m.Font(12)).TextColor(pal.muted).TextAlign(ui.Center).MaxLines(2)
				case p.Phase == "error":
					ui.Text(c, textutil.FirstNonEmpty(p.Title, tr("Connection incomplete"))).FontSize(m.Font(12)).TextColor(pal.muted).TextAlign(ui.Center).MaxLines(2)
				default:
					if !p.Unlock.Locked {
						ui.Spinner(c)
					}
					ui.Text(c, textutil.FirstNonEmpty(p.Status, p.Title)).FontSize(m.Font(12)).TextColor(pal.muted).TextAlign(ui.Center).MaxLines(2)
				}
			})
		})
		// The close button sits over the corner as a sibling, so its press
		// never starts a drag of the window.
		close := m.phoneButton(c.Key("desktop-float-close")).Absolute().Left(f.x+w-34).Top(f.y-10).Size(44, 44).Padding(0).
			Justify(ui.Center).AlignItems(ui.Center).ZIndex(1).Label(tr("End remote desktop"))
		close.Children(func() {
			ui.Row(c).Size(26, 26).Radius(13).Background(pal.ink).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				widgets.Icon(c, "x", 16, pal.Bg)
			})
		})
		if close.Clicked() {
			m.CloseDesktopPage(false)
		}
	})
}

// desktopFloatInput drags the window, snapping to the nearer side, and
// treats a press without a drag as a tap.
func (m *App) desktopFloatInput(p *MobileDesktopPage, ev ui.InputEvent, minX, maxX, minY, maxY float32) bool {
	f := &p.float
	switch ev.Kind {
	case ui.InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		f.pressing, f.dragged = true, false
		f.startX, f.startY, f.fromX, f.fromY = ev.X, ev.Y, f.x, f.y
		return true
	case ui.InputPointerMove:
		if !f.pressing {
			return false
		}
		dx, dy := ev.X-f.startX, ev.Y-f.startY
		if !f.dragged && desktopDist(dx, dy) < 8 {
			return true
		}
		f.dragged = true
		f.x = desktopClamp(f.fromX+dx, minX, max(minX, maxX))
		f.y = desktopClamp(f.fromY+dy, minY, max(minY, maxY))
		uiruntime.Invalidate()
		return true
	case ui.InputPointerUp, ui.InputPointerCancel:
		if !f.pressing {
			return false
		}
		f.pressing = false
		if f.dragged {
			if f.x-minX < maxX-f.x {
				f.x = minX
			} else {
				f.x = maxX
			}
			return true
		}
		if ev.Kind == ui.InputPointerCancel || ev.Canceled {
			return true
		}
		if p.Phase == "ended" {
			m.CloseDesktopPage(false)
		} else {
			m.DesktopRestore(p)
		}
		return true
	}
	return false
}
