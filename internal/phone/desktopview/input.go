package desktopview

import (
	"math"
	"miya/internal/desktop"
	"time"
)

type desktopPointer struct {
	active                       bool
	startX, startY, lastX, lastY float32
	at                           time.Time
	moved, drag                  bool
	from                         Point
	seq                          int
}
type Mapper struct {
	Active, Blocked  bool
	Width, Height    float32
	View             Rect
	Zoom, PanX, panY float32
	Trackpad         bool
	TouchMode        int
	Cursor           Point
	pointer          desktopPointer
	lastTap          time.Time
	scrollX, scrollY float32
	Overlays         []Rect
	Send             func(desktop.Input)
	After            func(time.Duration, func())
}

func (p *Mapper) Reset() {
	p.pointer = desktopPointer{seq: p.pointer.seq + 1}
	p.lastTap = time.Time{}
	p.scrollX, p.scrollY = 0, 0
}
func (p *Mapper) Dragging() bool    { return p.pointer.active && p.pointer.drag }
func (p *Mapper) PanY() float32     { return p.panY }
func (p *Mapper) SetPanY(v float32) { p.panY = v }
func desktopClampInt(v int) int     { return max(-1000, min(1000, v)) }

func desktopClamp(v, lo, hi float32) float32 { return max(lo, min(hi, v)) }

func desktopDist(dx, dy float32) float32 {
	return float32(math.Hypot(float64(dx), float64(dy)))
}

// imageRect is where the frame shows inside the viewport, in local DIPs.
func (p *Mapper) ImageRect() (Rect, bool) {
	if p.Width <= 0 || p.Height <= 0 {
		return Rect{}, false
	}
	iw, ih := p.Width, p.Height
	vw, vh := p.View.W, p.View.H
	if iw <= 0 || ih <= 0 || vw <= 0 || vh <= 0 {
		return Rect{}, false
	}
	s := min(vw/iw, vh/ih) * p.Zoom
	w, h := iw*s, ih*s
	maxX, maxY := max(0, (w-p.View.W)/2), max(0, (h-p.View.H)/2)
	p.PanX, p.panY = desktopClamp(p.PanX, -maxX, maxX), desktopClamp(p.panY, -maxY, maxY)
	return Rect{X: (p.View.W-w)/2 + p.PanX, Y: (p.View.H-h)/2 + p.panY, W: w, H: h}, true
}

func (p *Mapper) point(x, y float32) (Point, bool) {
	r, ok := p.ImageRect()
	if !ok || !r.Contains(x, y) {
		return Point{}, false
	}
	return Point{X: float64(desktopClamp((x-r.X)/r.W, 0, 1)), Y: float64(desktopClamp((y-r.Y)/r.H, 0, 1))}, true
}

func (p *Mapper) SetZoom(z float32) {
	p.Zoom = desktopClamp(z, 1, 4)
}

// moveCursor accelerates faster swipes, as the previous page did.
func (p *Mapper) moveCursor(dx, dy float32) {
	r, ok := p.ImageRect()
	if !ok {
		return
	}
	gain := 1.1 + min(1.4, desktopDist(dx, dy)/24)
	p.Cursor.X = float64(desktopClamp(float32(p.Cursor.X)+dx*gain/r.W, 0, 1))
	p.Cursor.Y = float64(desktopClamp(float32(p.Cursor.Y)+dy*gain/r.H, 0, 1))
	// Keep the cursor on screen when zoomed in.
	cx, cy := r.X+float32(p.Cursor.X)*r.W, r.Y+float32(p.Cursor.Y)*r.H
	const margin = 24
	if cx < margin {
		p.PanX += margin - cx
	} else if cx > p.View.W-margin {
		p.PanX -= cx - (p.View.W - margin)
	}
	if cy < margin {
		p.panY += margin - cy
	} else if cy > p.View.H-margin {
		p.panY -= cy - (p.View.H - margin)
	}
}

func (p *Mapper) AtCursor(kind string) {
	p.Send(desktop.Input{Kind: kind, X: p.Cursor.X, Y: p.Cursor.Y})
}

func (p *Mapper) Input(ev InputEvent) bool {
	if !p.Active || p.Width <= 0 || p.Height <= 0 || p.Blocked {
		return false
	}
	g := &p.pointer
	switch ev.Kind {
	case InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		for _, o := range p.Overlays {
			if o.Contains(ev.X, ev.Y) {
				return false
			}
		}
		*g = desktopPointer{active: true, startX: ev.X, startY: ev.Y, lastX: ev.X, lastY: ev.Y, at: time.Now(), from: p.Cursor, seq: g.seq + 1}
		if p.Trackpad {
			seq := g.seq
			p.After(450*time.Millisecond, func() {
				if p.pointer.active && p.pointer.seq == seq && !p.pointer.moved {
					p.pointer.drag = true
				}
			})
		}
		return true
	case InputPointerMove:
		if !g.active {
			return false
		}
		if !g.moved {
			if desktopDist(ev.X-g.startX, ev.Y-g.startY) < 8 {
				return true
			}
			g.moved = true
		}
		dx, dy := ev.X-g.lastX, ev.Y-g.lastY
		g.lastX, g.lastY = ev.X, ev.Y
		if p.Trackpad {
			p.moveCursor(dx, dy)
			p.AtCursor("move")
		} else if p.TouchMode == 2 {
			p.PanX, p.panY = p.PanX+dx, p.panY+dy
		} else if p.TouchMode == 0 {
			if at, ok := p.point(g.startX, g.startY); ok {
				p.Scroll(at, dx*2, dy*2)
			}
		}
		return true
	case InputPointerUp:
		if !g.active {
			return false
		}
		down := *g
		g.active = false
		if ev.Canceled {
			// The system or a touch gesture took the press: nothing it did
			// is sent to the desktop.
			return true
		}
		if p.Trackpad {
			if down.drag {
				if down.moved {
					p.Send(desktop.Input{Kind: "drag", X: down.from.X, Y: down.from.Y, ToX: p.Cursor.X, ToY: p.Cursor.Y})
				}
				return true
			}
			if !down.moved && time.Since(down.at) < 400*time.Millisecond {
				kind := "click"
				if time.Since(p.lastTap) < 350*time.Millisecond {
					kind, p.lastTap = "doubleClick", time.Time{}
				} else {
					p.lastTap = time.Now()
				}
				p.AtCursor(kind)
			}
			return true
		}
		start, startOK := p.point(down.startX, down.startY)
		end, endOK := p.point(ev.X, ev.Y)
		dx, dy := ev.X-down.startX, ev.Y-down.startY
		switch {
		case p.TouchMode == 2:
		case p.TouchMode == 1:
			if startOK && endOK {
				p.Send(desktop.Input{Kind: "drag", X: start.X, Y: start.Y, ToX: end.X, ToY: end.Y})
			}
		case desktopDist(dx, dy) > 10:
			// Moves stream scrolls while the finger is down. Only a release
			// without move events needs the full displacement as a fallback.
			if startOK && !down.moved {
				p.Scroll(start, dx*2, dy*2)
			}
		case endOK && !down.moved:
			p.Send(desktop.Input{Kind: "click", X: end.X, Y: end.Y})
		}
		return true
	case InputScroll:
		// UIKit's keyboard container and touch-scroll arbitration deliver
		// scrolls directly, without a pointer-up. Their deltas are offsets;
		// CGEvent takes the inverse, matching a finger dragging the content.
		switch {
		case p.Trackpad:
			p.Scroll(p.Cursor, -ev.DX*2, -ev.DY*2)
		case p.TouchMode == 0:
			if at, ok := p.point(ev.X, ev.Y); ok {
				p.Scroll(at, -ev.DX*2, -ev.DY*2)
			}
		case p.TouchMode == 2:
			p.PanX, p.panY = p.PanX-ev.DX, p.panY-ev.DY
		}
		return true
	case InputZoom:
		if ev.Scale > 0 {
			p.SetZoom(p.Zoom * ev.Scale)
		}
		return true
	}
	return false
}

func (p *Mapper) Scroll(at Point, dx, dy float32) {
	p.scrollX, p.scrollY = p.scrollX+dx, p.scrollY+dy
	x, y := int(p.scrollX), int(p.scrollY)
	if x == 0 && y == 0 {
		return
	}
	p.scrollX, p.scrollY = p.scrollX-float32(x), p.scrollY-float32(y)
	p.Send(desktop.Input{Kind: "scroll", X: at.X, Y: at.Y, DeltaX: desktopClampInt(x), DeltaY: desktopClampInt(y)})
}
