package mobile

import (
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/textutil"
	"miya/internal/widgets"
)

// desktopFloat is the in-app floating window of a minimized remote desktop.
// The lease stays open and the screen keeps refreshing, but no touch on the
// floating window reaches the Mac: tapping it restores the full page.
type desktopFloat struct {
	placed bool
	offset ui.Point
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
		f.offset, f.placed = ui.Point{X: maxX, Y: max(minY, maxY-96)}, true
	}
	ended := p.Phase == "ended"
	ui.Overlay(c, func() {
		label := tr("Remote desktop floating window. Tap to return; drag to move.")
		if ended {
			label = tr("Control session ended. Tap to close.")
		}
		// Draggable owns the entire touch until release, even after the window
		// moves away from where the touch began. Its transform also moves the
		// close button and lets the edge spring settle without rebuilding chat.
		bounds := ui.Rect{X: minX, Y: minY, W: max(0, maxX-minX), H: max(0, maxY-minY)}
		window := ui.Column(c.Key("desktop-float")).Absolute().Left(0).Top(0).Width(w).Height(h).
			Label(label).Draggable(&f.offset, ui.DraggableOptions{
			Bounds: &bounds, Rigid: true,
			Snap: func(at ui.Point) ui.Point {
				if at.X-minX < maxX-at.X {
					at.X = minX
				} else {
					at.X = maxX
				}
				return at
			},
		})
		window.Children(func() {
			card := ui.Column(c.Key("desktop-float-card")).Fill().Radius(12).Clip().
				Background(pal.surface).Border(1, pal.line)
			if !window.Dragging() {
				card.Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.3))
			}
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
					case p.Phase == "error" || p.Phase == "paused" && !p.Busy:
						ui.Text(c, textutil.FirstNonEmpty(p.Title, tr("Connection incomplete"))).FontSize(m.Font(12)).TextColor(pal.muted).TextAlign(ui.Center).MaxLines(2)
					default:
						if !p.Unlock.Locked {
							ui.Spinner(c)
						}
						ui.Text(c, textutil.FirstNonEmpty(p.Status, p.Title)).FontSize(m.Font(12)).TextColor(pal.muted).TextAlign(ui.Center).MaxLines(2)
					}
				})
			})
			// A separate hit target keeps a close-button press from dragging. It
			// shares the parent transform, including while the edge spring settles.
			close := m.phoneButton(c.Key("desktop-float-close")).Absolute().Left(w-34).Top(-10).Size(44, 44).Padding(0).
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
		if window.Clicked() {
			if ended {
				m.CloseDesktopPage(false)
			} else {
				m.DesktopRestore(p)
			}
		}
	})
}
