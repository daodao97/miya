package desktopview

import (
	"miya/internal/desktop"
	"testing"
	"time"
)

func TestTwoFingerGesturesCancelClickAndPreserveZoom(t *testing.T) {
	var sent []desktop.Input
	p := &Mapper{Active: true, Width: 400, Height: 300, View: Rect{W: 400, H: 300}, Zoom: 1, Trackpad: true, Cursor: Point{.5, .5}, Send: func(in desktop.Input) { sent = append(sent, in) }, After: func(time.Duration, func()) {}}
	tm := &TouchMapper{Mapper: p, Input: func(e InputEvent) { p.Input(e) }, Scroll: func(dx, dy float32) { p.Scroll(p.Cursor, dx, dy) }, RightClick: func() { p.AtCursor("rightClick") }}
	tm.Update(1, []Touch{{ID: 1, X: 100, Y: 100, Changed: true}})
	tm.Update(1, []Touch{{ID: 1, X: 100, Y: 100}, {ID: 2, X: 200, Y: 100, Changed: true}})
	tm.Update(2, []Touch{{ID: 1, X: 50, Y: 90, Changed: true}, {ID: 2, X: 250, Y: 90, Changed: true}})
	tm.Update(3, []Touch{{ID: 1, X: 50, Y: 90, Changed: true}, {ID: 2, X: 250, Y: 90}})
	tm.Update(3, []Touch{{ID: 2, X: 250, Y: 90, Changed: true}})
	if p.Zoom != 2 || len(sent) != 1 || sent[0].Kind != "scroll" || sent[0].DeltaY != -20 {
		t.Fatal("two fingers clicked, dragged or lost pinch/scroll", p.Zoom, sent)
	}
	if tm.Active() {
		t.Fatal("fingers retained after release")
	}
}
func TestResetCannotActivateAnOldHold(t *testing.T) {
	var holds []func()
	p := &Mapper{Active: true, Width: 400, Height: 300, View: Rect{W: 400, H: 300}, Zoom: 1, Trackpad: true, Send: func(desktop.Input) {}, After: func(_ time.Duration, f func()) { holds = append(holds, f) }}
	p.Input(InputEvent{Kind: InputPointerDown})
	p.Reset()
	p.Input(InputEvent{Kind: InputPointerDown})
	holds[0]()
	if p.Dragging() {
		t.Fatal("old hold activated new pointer")
	}
	holds[1]()
	if !p.Dragging() {
		t.Fatal("new hold did not activate drag")
	}
}
