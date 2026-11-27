package desktopui

import (
	"context"
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
)

// imageRect is used for both rendering and sending pointer coordinates.
func (p *peerDesktop) imageRect() ui.Rect {
	w, h := ui.ImageSize(p.image)
	if w <= 0 || h <= 0 || p.viewport.W <= 0 || p.viewport.H <= 0 {
		return ui.Rect{}
	}
	scale := min(p.viewport.W/w, p.viewport.H/h) * max(1, p.zoom)
	w, h = w*scale, h*scale
	maxX, maxY := max(0, (w-p.viewport.W)/2), max(0, (h-p.viewport.H)/2)
	return ui.Rect{
		X: (p.viewport.W-w)/2 + max(-maxX, min(maxX, p.panX)),
		Y: (p.viewport.H-h)/2 + max(-maxY, min(maxY, p.panY)),
		W: w, H: h,
	}
}

func (p *peerDesktop) point(x, y float32, clamp bool) (float64, float64, bool) {
	r := p.imageRect()
	if r.W <= 0 || r.H <= 0 {
		return 0, 0, false
	}
	nx, ny := (x-r.X)/r.W, (y-r.Y)/r.H
	if !clamp && (nx < 0 || nx > 1 || ny < 0 || ny > 1) {
		return 0, 0, false
	}
	return float64(max(0, min(1, nx))), float64(max(0, min(1, ny))), true
}

func (p *peerDesktop) handleInput(ev ui.InputEvent) bool {
	if p.busy || !p.live || p.image == nil || p.locked || p.picker != "" || p.session.FrameStream && p.stream == nil {
		return false
	}
	switch ev.Kind {
	case ui.InputPointerDown:
		if _, _, ok := p.point(ev.X, ev.Y, false); !ok || ev.Button < 0 || ev.Button > 1 {
			return false
		}
		p.focus.Focus()
		p.press = &ev
		return true
	case ui.InputPointerMove:
		x, y, ok := p.point(ev.X, ev.Y, false)
		if ok && ev.Button < 0 && p.press == nil {
			p.enqueue(desktop.Input{Kind: "move", X: x, Y: y})
		}
		return ok || p.press != nil
	case ui.InputPointerUp:
		press := p.press
		p.press = nil
		if press == nil || ev.Canceled || press.Button != ev.Button {
			return false
		}
		x, y, ok := p.point(press.X, press.Y, false)
		if !ok {
			return true
		}
		toX, toY, _ := p.point(ev.X, ev.Y, true)
		kind := "click"
		switch {
		case ev.Button == 1:
			kind = "rightClick"
		case math.Hypot(float64(ev.X-press.X), float64(ev.Y-press.Y)) > 5:
			kind = "drag"
		case press.Clicks > 1:
			kind = "doubleClick"
		}
		p.enqueue(desktop.Input{Kind: kind, X: x, Y: y, ToX: toX, ToY: toY})
		return true
	case ui.InputScroll:
		x, y, ok := p.point(ev.X, ev.Y, false)
		if ok {
			// Precise trackpad events often carry less than one pixel. Convert
			// after accumulating, and avoid sending zero-distance events.
			p.scrollX, p.scrollY = p.scrollX-ev.DX, p.scrollY-ev.DY
			dx, dy := int(p.scrollX), int(p.scrollY)
			p.scrollX, p.scrollY = p.scrollX-float32(dx), p.scrollY-float32(dy)
			if dx != 0 || dy != 0 {
				p.enqueue(desktop.Input{Kind: "scroll", X: x, Y: y, DeltaX: peerScroll(dx), DeltaY: peerScroll(dy)})
			}
		}
		return ok
	case ui.InputZoom:
		x, y, ok := p.point(ev.X, ev.Y, false)
		if !ok || p.press != nil || ev.Scale <= 0 || ev.Scale == 1 {
			return false
		}
		r := p.imageRect()
		oldZoom := max(1, p.zoom)
		p.zoom = min(4, max(1, oldZoom*ev.Scale))
		ratio := p.zoom / oldZoom
		p.panX = ev.X - p.viewport.W/2 - (float32(x)-0.5)*r.W*ratio
		p.panY = ev.Y - p.viewport.H/2 - (float32(y)-0.5)*r.H*ratio
		return true
	case ui.InputText:
		p.enqueue(desktop.Input{Kind: "text", Text: ev.Text})
		return true
	case ui.InputKeyDown:
		in, ok := peerKey(ev)
		if ok {
			p.enqueue(in)
		}
		return ok
	}
	return false
}

var peerNavigationKeys = map[ui.Key]string{
	ui.KeyEnter: "enter", ui.KeyEscape: "escape", ui.KeyTab: "tab",
	ui.KeyBackspace: "backspace", ui.KeyDelete: "delete",
	ui.KeyLeft: "left", ui.KeyRight: "right", ui.KeyUp: "up", ui.KeyDown: "down",
}

var peerShortcutKeys = map[ui.Key]string{
	ui.KeyA: "a", ui.KeyC: "c", ui.KeyV: "v", ui.KeyZ: "z", ui.KeyL: "l",
}

func peerKey(ev ui.InputEvent) (desktop.Input, bool) {
	key := peerNavigationKeys[ev.Key]
	if key == "" && (ev.Mods == ui.Ctrl || ev.Mods == ui.Super) {
		key = peerShortcutKeys[ev.Key]
	}
	modifier := ""
	switch ev.Mods {
	case 0:
	case ui.Super:
		modifier = "command"
	case ui.Ctrl:
		modifier = "control"
		if ui.Cmd == ui.Ctrl && peerShortcutKeys[ev.Key] != "" {
			modifier = "command"
		}
	case ui.Alt:
		modifier = "option"
	case ui.Shift:
		modifier = "shift"
	default:
		return desktop.Input{}, false
	}
	return desktop.Input{Kind: "key", Key: key, Modifier: modifier}, key != ""
}

func peerScroll(delta int) int { return max(-1000, min(1000, delta)) }

func (p *peerDesktop) discardInput() {
	p.queue, p.press = nil, nil
	p.scrollX, p.scrollY = 0, 0
	p.scrollingUntil = time.Time{}
}

func (p *peerDesktop) enqueue(in desktop.Input) {
	if in.Validate() != nil {
		return
	}
	if in.Kind == "scroll" {
		p.scrollingUntil = time.Now().Add(450 * time.Millisecond)
	}
	// Scrolling moves the remote pointer itself. A pending hover at the
	// same point should not interrupt coalescing a trackpad gesture.
	if n := len(p.queue); n > 0 && in.Kind == "scroll" {
		last := p.queue[n-1]
		if last.Kind == "move" && in.X == last.X && in.Y == last.Y {
			p.queue = p.queue[:n-1]
		}
	}
	if n := len(p.queue); n > 0 && p.queue[n-1].Kind == in.Kind {
		switch in.Kind {
		case "move":
			p.queue[n-1] = in
			return
		case "scroll":
			in.DeltaX = peerScroll(in.DeltaX + p.queue[n-1].DeltaX)
			in.DeltaY = peerScroll(in.DeltaY + p.queue[n-1].DeltaY)
			p.queue[n-1] = in
			return
		}
	}
	if len(p.queue) >= 64 {
		p.status = tr("Input is too frequent")
		return
	}
	p.queue = append(p.queue, in)
	p.sendInput()
}

func (p *peerDesktop) sendInput() {
	if p.sending || p.busy || len(p.queue) == 0 {
		return
	}
	in, display, client, ctx, stream := p.queue[0], p.display, p.client, p.ctx, p.stream
	p.queue[0] = desktop.Input{}
	p.queue = p.queue[1:]
	p.sending = true
	go func() {
		inputCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		attempted := time.Now()
		send := client.InputForDisplay
		if stream != nil {
			send = stream.SendInput
		}
		err := send(inputCtx, in, display)
		// A 429 rejects the event before dispatch. Retrying it preserves
		// gesture order without replaying ambiguous delivery failures.
		for retry := 0; retry < 2 && control.Status(err) == 429 && control.Wait(inputCtx, 35*time.Millisecond); retry++ {
			attempted = time.Now()
			err = send(inputCtx, in, display)
		}
		// Avoid adding a fixed delay after a network round trip that
		// already exceeded the host's minimum event spacing.
		if remaining := time.Until(attempted.Add(22 * time.Millisecond)); remaining > 0 {
			control.Wait(ctx, remaining)
		}
		p.apply(ctx, func() {
			p.sending = false
			defer p.advance()
			if stream != nil && stream != p.stream {
				return
			}
			if err == nil && in.Kind == "scroll" && p.live && !p.busy && p.image != nil {
				p.scrollingUntil = time.Now().Add(450 * time.Millisecond)
			}
			if err != nil && control.Status(err) != 429 {
				p.discardInput()
				p.showError(err)
				return
			}
			p.sendInput()
		})
	}()
}
