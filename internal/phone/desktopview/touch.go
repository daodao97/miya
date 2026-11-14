package desktopview

import (
	"math"
	"time"
)

type Touch struct {
	ID      int
	X, Y    float32
	Changed bool
}

// TouchMapper arbitrates one-finger input against two-finger scroll/pinch.
// A second finger cancels the pending click/drag; lifting it never starts a
// new one-finger gesture. Coordinates are local to the remote viewport.
type TouchMapper struct {
	Mapper                 *Mapper
	Input                  func(InputEvent)
	Scroll                 func(float32, float32)
	RightClick             func()
	fingers                map[int]Touch
	multi, moved           bool
	at                     time.Time
	lastX, lastY, distance float32
}

func (t *TouchMapper) Active() bool { return len(t.fingers) > 0 }
func (t *TouchMapper) Update(phase int, touches []Touch) {
	if t.fingers == nil {
		t.fingers = map[int]Touch{}
	}
	before := len(t.fingers)
	if phase == 4 {
		t.Input(InputEvent{Kind: InputPointerUp, Canceled: true})
		clear(t.fingers)
		t.multi = false
		return
	}
	for _, p := range touches {
		if phase == 3 && p.Changed {
			delete(t.fingers, p.ID)
		} else {
			t.fingers[p.ID] = p
		}
	}
	n := len(t.fingers)
	if phase == 1 && before == 0 {
		t.multi, t.moved = false, false
		t.at = time.Now()
		for _, p := range t.fingers {
			t.Input(InputEvent{Kind: InputPointerDown, X: p.X, Y: p.Y})
			break
		}
	}
	if n >= 2 {
		var first, second Touch
		i := 0
		for _, p := range t.fingers {
			if i == 0 {
				first = p
			}
			if i == 1 {
				second = p
			}
			i++
		}
		x, y := (first.X+second.X)/2, (first.Y+second.Y)/2
		distance := float32(math.Hypot(float64(first.X-second.X), float64(first.Y-second.Y)))
		if !t.multi || before != n {
			t.Input(InputEvent{Kind: InputPointerUp, Canceled: true})
			t.multi = true
		} else if phase == 2 {
			dx, dy := x-t.lastX, y-t.lastY
			if float32(math.Hypot(float64(dx), float64(dy))) > 1 || float32(math.Abs(float64(distance-t.distance))) > 1 {
				t.moved = true
			}
			scale := float32(1)
			if t.distance > 0 {
				scale = distance / t.distance
			}
			if t.Mapper.Trackpad {
				t.Scroll(dx*2, dy*2)
			} else {
				t.Mapper.PanX += dx
				t.Mapper.panY += dy
			}
			t.Mapper.SetZoom(t.Mapper.Zoom * scale)
		}
		t.lastX, t.lastY, t.distance = x, y, distance
		return
	}
	if t.multi {
		if n == 0 {
			if !t.moved && time.Since(t.at) < 400*time.Millisecond && t.Mapper.Trackpad {
				t.RightClick()
			}
			t.multi = false
		}
		return
	}
	for _, p := range touches {
		if !p.Changed {
			continue
		}
		kind := InputPointerMove
		if phase == 3 {
			kind = InputPointerUp
		}
		if phase == 2 || phase == 3 {
			t.Input(InputEvent{Kind: kind, X: p.X, Y: p.Y})
		}
	}
}
