package nsui

import (
	"context"
	"math"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/phone/desktopview"
)

func (p *peerDesktop) screen(cx *core.Scope, ready func() bool) core.Node {
	ref := new(core.Ref)
	view := core.NewSignal(cx, core.Size{})
	zoom := core.NewSignal(cx, float32(1))
	pan := core.NewSignal(cx, core.Point{})
	var press *core.MouseButtonEvent
	var scroll core.Point
	rect := func() desktopview.Rect {
		f := p.frame.Get()
		if f == nil {
			return desktopview.Rect{}
		}
		w, h := float32(view.Get().Width), float32(view.Get().Height)
		scale := min(w/f.Width, h/f.Height) * zoom.Get()
		iw, ih := f.Width*scale, f.Height*scale
		dx, dy := max(0, (iw-w)/2), max(0, (ih-h)/2)
		return desktopview.Rect{X: (w-iw)/2 + min(dx, max(-dx, float32(pan.Get().X))), Y: (h-ih)/2 + min(dy, max(-dy, float32(pan.Get().Y))), W: iw, H: ih}
	}
	point := func(at core.Point, clamp bool) (float64, float64, bool) {
		r := rect()
		if !ready() || r.W == 0 || r.H == 0 || !clamp && !r.Contains(float32(at.X), float32(at.Y)) {
			return 0, 0, false
		}
		x, y := (float32(at.X)-r.X)/r.W, (float32(at.Y)-r.Y)/r.H
		return float64(max(0, min(1, x))), float64(max(0, min(1, y))), true
	}
	return core.View{TestID: "peer-screen", Ref: ref, Label: tr("Remote desktop"), Focusable: ready,
		Style:    ui.FillRest.Merge(core.Style{Background: core.RGB(0x111113), Overflow: core.Hidden, CornerRadius: 8}),
		OnLayout: func(b core.Bounds) { view.Set(b.Size) },
		OnBlur:   func() { press = nil },
		OnMouseDown: func(e core.MouseButtonEvent) {
			if _, _, ok := point(e.Position, false); ok && (e.Button == core.MouseButtonLeft || e.Button == core.MouseButtonRight) {
				ref.Focus()
				press = &e
			}
		},
		OnMouseUp: func(e core.MouseButtonEvent) {
			down := press
			press = nil
			if down == nil || e.Clicks == 0 || down.Button != e.Button {
				return
			}
			x, y, ok := point(down.Position, false)
			if !ok {
				return
			}
			toX, toY, _ := point(e.Position, true)
			kind := "click"
			switch {
			case e.Button == core.MouseButtonRight:
				kind = "rightClick"
			case math.Hypot(float64(e.Position.X-down.Position.X), float64(e.Position.Y-down.Position.Y)) > 5:
				kind = "drag"
			case down.Clicks > 1:
				kind = "doubleClick"
			}
			p.enqueue(desktop.Input{Kind: kind, X: x, Y: y, ToX: toX, ToY: toY})
		},
		OnMouseMove: func(e core.MouseEvent) {
			if x, y, ok := point(e.Position, false); ok && press == nil && e.PressedButton == core.MouseButtonNone {
				p.enqueue(desktop.Input{Kind: "move", X: x, Y: y})
			}
		},
		OnWheel: func(e core.WheelEvent) {
			if x, y, ok := point(e.Position, false); ok {
				if e.Modifiers.Alt && zoom.Get() > 1 {
					pan.Update(func(v core.Point) core.Point { return core.Point{X: v.X - e.Delta.X, Y: v.Y - e.Delta.Y} })
					return
				}
				scroll.X -= e.Delta.X
				scroll.Y -= e.Delta.Y
				dx, dy := int(scroll.X), int(scroll.Y)
				scroll.X -= core.Points(dx)
				scroll.Y -= core.Points(dy)
				if dx != 0 || dy != 0 {
					p.enqueue(desktop.Input{Kind: "scroll", X: x, Y: y, DeltaX: clampPeerDelta(dx), DeltaY: clampPeerDelta(dy)})
				}
			}
		},
		OnPinch: func(e core.GestureEvent) {
			if _, _, ok := point(e.Position, false); ok && e.Value > 0 && press == nil {
				zoom.Update(func(z float32) float32 { return max(1, min(4, z*e.Value)) })
			}
		},
		OnKeyDown: func(e core.KeyEvent) bool {
			in, ok := peerKeyInput(e)
			return ready() && ok && p.enqueue(in)
		},
		Children: []core.Node{core.Image{
			SourceFunc: func() core.ImageSource {
				if f := p.frame.Get(); f != nil {
					return core.ImageSource{Data: f.Data, Size: core.Size{Width: core.Points(f.Width), Height: core.Points(f.Height)}}
				}
				return core.ImageSource{}
			},
			StyleFunc: func() core.Style {
				r := rect()
				return core.Style{Position: core.Absolute, Left: core.Pt(core.Points(r.X)), Top: core.Pt(core.Points(r.Y)), Width: core.Pt(core.Points(r.W)), Height: core.Pt(core.Points(r.H))}
			},
		}},
	}
}

func peerKeyInput(e core.KeyEvent) (desktop.Input, bool) {
	in := desktop.Input{Kind: "key", Key: e.Key}
	m := e.Modifiers
	var n int
	for _, held := range []bool{m.Control, m.Platform, m.Alt, m.Shift} {
		if held {
			n++
		}
	}
	if n > 1 {
		return in, false
	}
	switch {
	case m.Platform:
		in.Modifier = "command"
	case m.Control:
		in.Modifier = "control"
	case m.Alt:
		in.Modifier = "option"
	case m.Shift:
		in.Modifier = "shift"
	}
	if len(e.Key) == 1 || e.Key == "space" {
		if m.Control && !m.Platform && e.Key != "space" {
			in.Modifier = "command"
		}
		if !m.Control && !m.Platform && !m.Alt {
			if e.Key == "space" {
				e.Key = " "
			} else if m.Shift && e.Key[0] >= 'a' && e.Key[0] <= 'z' {
				e.Key = string(e.Key[0] - 'a' + 'A')
			}
			in = desktop.Input{Kind: "text", Text: e.Key}
		}
	}
	return in, in.Validate() == nil
}

func clampPeerDelta(delta int) int { return max(-1000, min(1000, delta)) }

func (p *peerDesktop) enqueue(in desktop.Input) bool {
	if p.client == nil || p.frameCtx == nil || p.busy.Get() || p.locked.Get() || p.frame.Get() == nil || in.Validate() != nil {
		return false
	}
	if n := len(p.queue); n > 0 {
		switch last := p.queue[n-1]; {
		case last.Kind == "move" && in.Kind == "move":
			p.queue[n-1] = in
			return true
		case last.Kind == "scroll" && in.Kind == "scroll":
			in.DeltaX, in.DeltaY = clampPeerDelta(in.DeltaX+last.DeltaX), clampPeerDelta(in.DeltaY+last.DeltaY)
			p.queue[n-1] = in
			return true
		}
	}
	if len(p.queue) >= 64 {
		p.status.Set(tr("Input is too frequent"))
		return false
	}
	p.queue = append(p.queue, in)
	p.sendInput()
	return true
}

func (p *peerDesktop) sendInput() {
	if p.sending || p.busy.Get() || len(p.queue) == 0 || p.frameCtx == nil {
		return
	}
	in := p.queue[0]
	p.queue[0] = desktop.Input{}
	p.queue = p.queue[1:]
	p.sending = true
	ctx, client, stream, display := p.frameCtx, p.client, p.stream, p.display
	go func() {
		call, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		send := client.InputForDisplay
		if stream != nil {
			send = stream.SendInput
		}
		start := time.Now()
		err := send(call, in, display)
		for i := 0; i < 2 && control.Status(err) == 429 && control.Wait(call, 35*time.Millisecond); i++ {
			start = time.Now()
			err = send(call, in, display)
		}
		control.Wait(call, time.Until(start.Add(22*time.Millisecond)))
		p.cx.Post(func() {
			p.sending = false
			if err != nil && control.Status(err) == 410 && p.frameCtx == ctx {
				p.showError(err)
				return
			}
			if pending := p.pendingWork; pending != nil {
				p.pendingWork = nil
				pending()
				return
			}
			if p.frameCtx != ctx {
				return
			}
			if err != nil && control.Status(err) != 429 {
				p.queue = nil
				p.showError(err)
				return
			}
			p.sendInput()
		})
	}()
}
