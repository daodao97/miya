package mobile

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDesktopPinchKeepsZoomAfterRelease(t *testing.T) {
	for _, windowOnly := range []bool{false, true} {
		for _, trackpad := range []bool{false, true} {
			p := &MobileDesktopPage{Zoom: 1, WindowOnly: windowOnly, Trackpad: trackpad}
			m := &App{}
			m.desktopGestures(p, []ui.GestureEvent{
				{Kind: "pinch", Phase: "begin", Scale: 1},
				{Kind: "pinch", Phase: "change", Scale: 1.5},
				{Kind: "pinch", Phase: "change", Scale: 2},
			})
			if p.Zoom != 3 {
				t.Fatalf("pinch did not enlarge the view: %v", p.Zoom)
			}
			// The backend resets its cumulative scale to 1 on release.
			m.desktopGestures(p, []ui.GestureEvent{{Kind: "pinch", Phase: "end", Scale: 1.0 / 3}})
			if p.Zoom != 3 {
				t.Fatalf("release reset zoom: window=%v trackpad=%v zoom=%v", windowOnly, trackpad, p.Zoom)
			}
			m.desktopGestures(p, []ui.GestureEvent{
				{Kind: "pinch", Phase: "begin", Scale: 1},
				{Kind: "pinch", Phase: "change", Scale: 0.5},
				{Kind: "pinch", Phase: "cancel", Scale: 2},
			})
			if p.Zoom != 1.5 {
				t.Fatalf("cancellation reset zoom: %v", p.Zoom)
			}
			m.desktopGestures(p, []ui.GestureEvent{{Kind: "pinch", Phase: "change", Scale: 0.1}})
			if p.Zoom != 1 {
				t.Fatalf("zoom did not stop at fit size: %v", p.Zoom)
			}
		}
	}
}

func TestDesktopPanKeepsPositionAfterRelease(t *testing.T) {
	p := &MobileDesktopPage{Zoom: 2}
	m := &App{}
	m.desktopGestures(p, []ui.GestureEvent{
		{Kind: "pan", Phase: "begin"},
		{Kind: "pan", Phase: "change", DX: 30, DY: 20},
		{Kind: "pan", Phase: "end", DX: -30, DY: -20},
	})
	if p.PanX != 30 || p.panY != 20 {
		t.Fatalf("release moved the view back: %v, %v", p.PanX, p.panY)
	}
}
