package integration

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDesktopPageFloatsKeepsLeaseAndRestores(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.Phase, p.Trackpad, p.Status = "live", true, "Connected · Full desktop"
	m.Router().Push("/desktop")
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	tt.Frame()
	if err := tt.Click("Show as floating window"); err != nil {
		t.Fatal(err)
	}
	for range 30 {
		tt.Frame()
	}
	if m.Router().Path() == "/desktop" || m.DesktopPage != p || !p.Floating || p.Phase != "live" {
		t.Fatal("minimizing should leave the page and keep the session", m.Router().Path(), p.Phase)
	}
	if _, ok := tt.Find("Remote desktop floating window. Tap to return; drag to move."); !ok {
		t.Fatal("floating window missing")
	}
	saveMobileNativeSnapshot(t, tt, "desktop-floating")
	if err := tt.Click("Remote desktop floating window. Tap to return; drag to move."); err != nil {
		t.Fatal(err)
	}
	for range 30 {
		tt.Frame()
	}
	if m.Router().Path() != "/desktop" || p.Floating || m.DesktopPage != p {
		t.Fatal("tapping the floating window should restore the page", m.Router().Path())
	}
	if _, ok := tt.Find("Remote desktop floating window. Tap to return; drag to move."); ok {
		t.Fatal("floating window should hide on the desktop page")
	}
	m.DesktopMinimize(p)
	for range 30 {
		tt.Frame()
	}
	if err := tt.Click("End remote desktop"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m.DesktopPage != nil {
		t.Fatal("closing the floating window should end control")
	}
}

func TestDesktopFloatDragFollowsPointerAcrossFrames(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.Floating = true
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	label := "Remote desktop floating window. Tap to return; drag to move."
	r, ok := tt.Find(label)
	if !ok {
		t.Fatal("floating window missing")
	}
	x, y := r.X+30, r.Y+30
	tt.Press(x, y)
	for _, distance := range []float32{25, 60, 150} {
		tt.Move(x-distance, y-20)
		tt.Frame()
		next, found := tt.Find(label)
		if !found || math.Abs(float64(next.X-(r.X-distance))) > 1 || math.Abs(float64(next.Y-(r.Y-20))) > 1 {
			t.Fatalf("drag at %v did not follow the pointer: initial=%v next=%v", distance, r, next)
		}
	}
	tt.Release(x-150, y-20)
	tt.Frame()
	next, _ := tt.Find(label)
	if math.Abs(float64(next.X-12)) > 1 || !p.Floating || len(p.Queue) != 0 {
		t.Fatalf("drag did not snap left safely: %v, floating=%v queue=%v", next, p.Floating, p.Queue)
	}
}

func TestDesktopFloatEndedSessionCloses(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.Phase = "live"
	m.Router().Push("/desktop")
	m.DesktopMinimize(p)
	m.DesktopFinish(p, "Remote control ended")
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	for range 30 {
		tt.Frame()
	}
	if m.DesktopPage != p {
		t.Fatal("an ended floating session should stay visible until dismissed")
	}
	if err := tt.Click("Control session ended. Tap to close."); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if m.DesktopPage != nil {
		t.Fatal("tapping the ended floating window should dismiss it")
	}
}
