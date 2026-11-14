package integration

import (
	"miya/internal/desktop"
	"miya/internal/desktop/control"
	mobile "miya/internal/phone/client"
	"miya/internal/phone/desktopview"
	"testing"
)

// livePhoneDesktopPage is a live page whose queue is held, so tests can read
// exactly what would be sent to the Mac.
func livePhoneDesktopPage(t *testing.T, m *mobile.Client) *mobile.MobileDesktopPage {
	t.Helper()
	img, err := desktopview.DecodeImage(windowJPEG(t))
	if err != nil {
		t.Fatal(err)
	}
	closed := 0
	openTestDesktopPage(m, &closed)
	p := m.DesktopPage
	// Stop pending hold and refresh timers before other tests swap runOnMain.
	t.Cleanup(p.Cancel)
	p.Active, p.Image, p.View, p.Zoom, p.Cursor, p.Sending = true, img, desktopview.Rect{W: 400, H: 300}, 1, desktopview.Point{X: .5, Y: .5}, true
	return p
}

func phoneTap(m *mobile.Client, p *mobile.MobileDesktopPage, x, y float32) {
	m.DesktopPointerInput(p, desktopview.InputEvent{Kind: desktopview.InputPointerDown, X: x, Y: y})
	m.DesktopPointerInput(p, desktopview.InputEvent{Kind: desktopview.InputPointerUp, X: x, Y: y})
}

func TestNSGoDesktopPageQueueCoalescesMovesAndScrolls(t *testing.T) {
	m := testPhoneClient()
	p := livePhoneDesktopPage(t, m)
	m.DesktopAtCursor(p, "move")
	p.Cursor = desktopview.Point{X: .7, Y: .2}
	m.DesktopAtCursor(p, "move")
	m.DesktopSend(p, phoneDesktopScroll(.1, .1, 30, -40))
	m.DesktopSend(p, phoneDesktopScroll(.2, .3, 990, -5))
	m.DesktopAtCursor(p, "click")
	m.DesktopAtCursor(p, "move")
	if len(p.Queue) != 4 {
		t.Fatal("unexpected queue", p.Queue)
	}
	if q := p.Queue[0]; q.Kind != "move" || q.X != .7 || q.Y != .2 {
		t.Fatal("moves did not keep the latest position", q)
	}
	if q := p.Queue[1]; q.Kind != "scroll" || q.DeltaX != 1000 || q.DeltaY != -45 || q.X != .2 {
		t.Fatal("scrolls did not add up within limits", q)
	}
	if p.Queue[2].Kind != "click" || p.Queue[3].Kind != "move" {
		t.Fatal("a click merged with surrounding moves", p.Queue)
	}
	for len(p.Queue) < 64 {
		m.DesktopAtCursor(p, "click")
	}
	m.DesktopAtCursor(p, "click")
	if len(p.Queue) != 64 || p.Status == "" {
		t.Fatal("queue was not bounded", len(p.Queue))
	}
}

func TestNSGoDesktopPageTouchModes(t *testing.T) {
	m := testPhoneClient()
	p := livePhoneDesktopPage(t, m)
	p.Trackpad = false
	r, ok := p.ImageRect()
	if !ok {
		t.Fatal("no image rect")
	}
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	phoneTap(m, p, cx, cy)
	if len(p.Queue) != 1 || p.Queue[0].Kind != "click" || p.Queue[0].X < .49 || p.Queue[0].X > .51 || p.Queue[0].Y < .49 || p.Queue[0].Y > .51 {
		t.Fatal("tap did not click the image point", p.Queue)
	}
	phoneTap(m, p, r.X-1, cy)
	if len(p.Queue) != 1 {
		t.Fatal("tap outside the image was sent")
	}
	p.Queue = nil
	m.DesktopPointerInput(p, desktopview.InputEvent{Kind: desktopview.InputPointerDown, X: cx, Y: cy})
	m.DesktopPointerInput(p, desktopview.InputEvent{Kind: desktopview.InputPointerMove, X: cx + 30, Y: cy - 20})
	m.DesktopPointerInput(p, desktopview.InputEvent{Kind: desktopview.InputPointerUp, X: cx + 30, Y: cy - 20})
	if len(p.Queue) != 1 || p.Queue[0].Kind != "scroll" || p.Queue[0].DeltaX != 60 || p.Queue[0].DeltaY != -40 {
		t.Fatal("swipe did not scroll", p.Queue)
	}
	p.Queue, p.TouchMode = nil, 1
	m.DesktopPointerInput(p, desktopview.InputEvent{Kind: desktopview.InputPointerDown, X: cx, Y: cy})
	m.DesktopPointerInput(p, desktopview.InputEvent{Kind: desktopview.InputPointerMove, X: cx + 30, Y: cy})
	m.DesktopPointerInput(p, desktopview.InputEvent{Kind: desktopview.InputPointerUp, X: cx + 30, Y: cy})
	if len(p.Queue) != 1 || p.Queue[0].Kind != "drag" || p.Queue[0].ToX <= p.Queue[0].X {
		t.Fatal("drag mode did not drag", p.Queue)
	}
	p.Queue, p.TouchMode = nil, 2
	p.SetZoom(2)
	m.DesktopPointerInput(p, desktopview.InputEvent{Kind: desktopview.InputPointerDown, X: cx, Y: cy})
	m.DesktopPointerInput(p, desktopview.InputEvent{Kind: desktopview.InputPointerMove, X: cx + 30, Y: cy})
	m.DesktopPointerInput(p, desktopview.InputEvent{Kind: desktopview.InputPointerUp, X: cx + 30, Y: cy})
	if len(p.Queue) != 0 || p.PanX == 0 {
		t.Fatal("pan mode sent input or did not move the view", p.Queue, p.PanX)
	}
}

func TestNSGoDesktopPageTrackpadTapsAndCursor(t *testing.T) {
	m := testPhoneClient()
	p := livePhoneDesktopPage(t, m)
	p.Trackpad = true
	phoneTap(m, p, 10, 10)
	phoneTap(m, p, 300, 250)
	if len(p.Queue) != 2 || p.Queue[0].Kind != "click" || p.Queue[1].Kind != "doubleClick" || p.Queue[0].X != .5 || p.Queue[0].Y != .5 {
		t.Fatal("trackpad taps did not click at the cursor", p.Queue)
	}
	p.Queue = nil
	m.DesktopPointerInput(p, desktopview.InputEvent{Kind: desktopview.InputPointerDown, X: 100, Y: 100})
	m.DesktopPointerInput(p, desktopview.InputEvent{Kind: desktopview.InputPointerMove, X: 140, Y: 100})
	m.DesktopPointerInput(p, desktopview.InputEvent{Kind: desktopview.InputPointerUp, X: 140, Y: 100})
	if p.Cursor.X <= .5 || len(p.Queue) != 1 || p.Queue[0].Kind != "move" || p.Queue[0].X != p.Cursor.X {
		t.Fatal("cursor did not follow the finger", p.Cursor, p.Queue)
	}
}

func TestNSGoDesktopPageFrameStatuses(t *testing.T) {
	m := testPhoneClient()
	p := livePhoneDesktopPage(t, m)
	p.Phase, p.Polling = "live", true
	m.ReceiveDesktopFrame(p, nil, nil, false, &control.HTTPError{Status: 403, Message: "请允许屏幕录制"})
	if p.Active || p.Phase != "error" || p.Title != "Permission required on Mac" || p.Polling {
		t.Fatal("permission failure did not stop control", p.Phase, p.Title)
	}
	p.Active, p.Phase = true, "live"
	m.ReceiveDesktopFrame(p, nil, nil, false, &control.HTTPError{Status: 410, Message: "Remote control ended"})
	if p.Active || p.Phase != "ended" || p.Image != nil || p.Detail != "Remote control ended" {
		t.Fatal("ended lease kept the page live", p.Phase)
	}
	if m.DesktopPage != p {
		t.Fatal("ended page should stay until the person leaves it")
	}
}

func phoneDesktopScroll(x, y float64, dx, dy int) (in desktop.Input) {
	in.Kind, in.X, in.Y, in.DeltaX, in.DeltaY = "scroll", x, y, dx, dy
	return
}
