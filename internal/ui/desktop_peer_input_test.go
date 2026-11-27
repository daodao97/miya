package desktopui

import (
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"miya/internal/desktop/control"
)

const peerScreenSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="200"><rect width="400" height="200" fill="black"/></svg>`

func TestPeerDesktopScreenFocusAndCoordinates(t *testing.T) {
	image, err := ui.ParseSVG([]byte(peerScreenSVG))
	if err != nil {
		t.Fatal(err)
	}
	// Hold the delivery worker so we can inspect the queued remote inputs.
	p := &peerDesktop{image: image, live: true, sending: true}
	tt := ui.NewTester(p.screen, 800, 600)
	box, ok := tt.Find("Remote desktop")
	if !ok {
		t.Fatal("remote screen has no hit target")
	}
	tt.ClickAt(box.X+box.W/2, box.Y+15)
	if len(p.queue) != 0 {
		t.Fatal("click in the letterbox reached the remote desktop")
	}
	if err := tt.Click("Remote desktop"); err != nil {
		t.Fatal(err)
	}
	tt.Type("你好")
	tt.Key(0, ui.KeyEnter)
	if len(p.queue) != 4 || p.queue[0].Kind != "move" {
		t.Fatalf("expected hover, click, committed text and Return, got %+v", p.queue)
	}
	click, text, key := p.queue[1], p.queue[2], p.queue[3]
	if click.Kind != "click" || click.X != 0.5 || click.Y != 0.5 {
		t.Errorf("center click: %+v", click)
	}
	if text.Kind != "text" || text.Text != "你好" || key.Kind != "key" || key.Key != "enter" {
		t.Errorf("keyboard input: %+v, %+v", text, key)
	}
	p.locked = true
	tt.Type("local")
	if len(p.queue) != 4 {
		t.Fatal("keyboard input reached the locked screen")
	}
}

func TestPeerDesktopTrackpad(t *testing.T) {
	image, err := ui.ParseSVG([]byte(peerScreenSVG))
	if err != nil {
		t.Fatal(err)
	}
	p := &peerDesktop{image: image, live: true, sending: true}
	tt := ui.NewTester(p.screen, 800, 600)
	box, ok := tt.Find("Remote desktop")
	if !ok {
		t.Fatal("remote screen has no hit target")
	}
	x, y := box.X+box.W*0.4, box.Y+box.H*0.5
	tt.Move(x, y)
	p.queue = nil
	for range 3 {
		tt.Wheel(x, y, -0.25, 0.25)
	}
	if len(p.queue) != 0 {
		t.Fatalf("subpixel scroll emitted premature events: %+v", p.queue)
	}
	for range 9 {
		tt.Wheel(x, y, -0.25, 0.25)
	}
	if len(p.queue) != 1 || p.queue[0].Kind != "scroll" || p.queue[0].DeltaX != 3 || p.queue[0].DeltaY != -3 {
		t.Fatalf("fractional trackpad gesture did not reach the input queue: %+v", p.queue)
	}
	now := time.Now()
	if options, interval, fast := p.frameProfile(now); fast || interval <= control.FrameInterval || options.MaxEdge != control.InteractiveFrameMaxEdge {
		t.Fatalf("old host scroll profile: %+v, %v, interactive=%v", options, interval, fast)
	}
	p.session.InteractiveFrames = true
	if options, interval, fast := p.frameProfile(now); !fast || interval >= control.FrameInterval || options.MaxEdge != control.InteractiveFrameMaxEdge {
		t.Fatalf("updated host scroll profile: %+v, %v, interactive=%v", options, interval, fast)
	}
	if options, _, fast := p.frameProfile(now.Add(time.Second)); fast || options.MaxEdge <= control.InteractiveFrameMaxEdge {
		t.Fatalf("idle viewer did not return to its normal profile: %+v, interactive=%v", options, fast)
	}
	p.discardInput()
	tt.Pinch(x, y, 2)
	if p.zoom != 2 || len(p.queue) != 0 {
		t.Fatalf("pinch should zoom the viewer on the local screen: zoom=%v queue=%+v", p.zoom, p.queue)
	}
	tt.RightClickAt(x, y)
	if len(p.queue) != 1 || p.queue[0].Kind != "rightClick" ||
		math.Abs(p.queue[0].X-0.4) > 0.0001 || p.queue[0].Y != 0.5 {
		t.Fatalf("pinch moved the point under the trackpad cursor: %+v", p.queue)
	}
	p.discardInput()
	tt.Press(x, y)
	tt.Move(x+80, y-40)
	tt.Release(x+80, y-40)
	if len(p.queue) != 1 || p.queue[0].Kind != "drag" ||
		math.Abs(p.queue[0].ToX-0.45) > 0.0001 || math.Abs(p.queue[0].ToY-0.45) > 0.0001 {
		t.Fatalf("drag did not use zoomed screen coordinates: %+v", p.queue)
	}
}
