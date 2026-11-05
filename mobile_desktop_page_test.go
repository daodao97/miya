package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/desktop"
	"miya/internal/desktop/control"
)

// liveTestDesktopPage is a live page whose queue is held, so tests can read
// exactly what would be sent to the Mac.
func liveTestDesktopPage(t *testing.T, m *mobileApp) *mobileDesktopPage {
	t.Helper()
	img, err := ui.DecodeImage(windowJPEG(t))
	if err != nil {
		t.Fatal(err)
	}
	closed := 0
	openTestDesktopPage(m, &closed)
	p := m.desktopPage
	// Stop pending hold and refresh timers before other tests swap runOnMain.
	t.Cleanup(p.cancel)
	p.active, p.image, p.view, p.zoom, p.cursor, p.sending = true, img, ui.Rect{W: 400, H: 300}, 1, desktopPoint{.5, .5}, true
	return p
}

func tap(m *mobileApp, p *mobileDesktopPage, x, y float32) {
	m.desktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerDown, X: x, Y: y})
	m.desktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerUp, X: x, Y: y})
}

func TestDesktopPageQueueCoalescesMovesAndScrolls(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	m.desktopAtCursor(p, "move")
	p.cursor = desktopPoint{.7, .2}
	m.desktopAtCursor(p, "move")
	m.desktopSend(p, desktopScroll(.1, .1, 30, -40))
	m.desktopSend(p, desktopScroll(.2, .3, 990, -5))
	m.desktopAtCursor(p, "click")
	m.desktopAtCursor(p, "move")
	if len(p.queue) != 4 {
		t.Fatal("unexpected queue", p.queue)
	}
	if q := p.queue[0]; q.Kind != "move" || q.X != .7 || q.Y != .2 {
		t.Fatal("moves did not keep the latest position", q)
	}
	if q := p.queue[1]; q.Kind != "scroll" || q.DeltaX != 1000 || q.DeltaY != -45 || q.X != .2 {
		t.Fatal("scrolls did not add up within limits", q)
	}
	if p.queue[2].Kind != "click" || p.queue[3].Kind != "move" {
		t.Fatal("a click merged with surrounding moves", p.queue)
	}
	for len(p.queue) < 64 {
		m.desktopAtCursor(p, "click")
	}
	m.desktopAtCursor(p, "click")
	if len(p.queue) != 64 || p.status == "" {
		t.Fatal("queue was not bounded", len(p.queue))
	}
}

func TestDesktopPageTouchModes(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.trackpad = false
	r, ok := p.imageRect()
	if !ok {
		t.Fatal("no image rect")
	}
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	tap(m, p, cx, cy)
	if len(p.queue) != 1 || p.queue[0].Kind != "click" || p.queue[0].X < .49 || p.queue[0].X > .51 || p.queue[0].Y < .49 || p.queue[0].Y > .51 {
		t.Fatal("tap did not click the image point", p.queue)
	}
	tap(m, p, r.X-1, cy)
	if len(p.queue) != 1 {
		t.Fatal("tap outside the image was sent")
	}
	p.queue = nil
	m.desktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerDown, X: cx, Y: cy})
	m.desktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerMove, X: cx + 30, Y: cy - 20})
	m.desktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerUp, X: cx + 30, Y: cy - 20})
	if len(p.queue) != 1 || p.queue[0].Kind != "scroll" || p.queue[0].DeltaX != 60 || p.queue[0].DeltaY != -40 {
		t.Fatal("swipe did not scroll", p.queue)
	}
	p.queue, p.touchMode = nil, 1
	m.desktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerDown, X: cx, Y: cy})
	m.desktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerMove, X: cx + 30, Y: cy})
	m.desktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerUp, X: cx + 30, Y: cy})
	if len(p.queue) != 1 || p.queue[0].Kind != "drag" || p.queue[0].ToX <= p.queue[0].X {
		t.Fatal("drag mode did not drag", p.queue)
	}
	p.queue, p.touchMode = nil, 2
	p.setZoom(2)
	m.desktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerDown, X: cx, Y: cy})
	m.desktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerMove, X: cx + 30, Y: cy})
	m.desktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerUp, X: cx + 30, Y: cy})
	if len(p.queue) != 0 || p.panX == 0 {
		t.Fatal("pan mode sent input or did not move the view", p.queue, p.panX)
	}
}

func TestDesktopPageTrackpadTapsAndCursor(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.trackpad = true
	tap(m, p, 10, 10)
	tap(m, p, 300, 250)
	if len(p.queue) != 2 || p.queue[0].Kind != "click" || p.queue[1].Kind != "doubleClick" || p.queue[0].X != .5 || p.queue[0].Y != .5 {
		t.Fatal("trackpad taps did not click at the cursor", p.queue)
	}
	p.queue = nil
	m.desktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerDown, X: 100, Y: 100})
	m.desktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerMove, X: 140, Y: 100})
	m.desktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerUp, X: 140, Y: 100})
	if p.cursor.X <= .5 || len(p.queue) != 1 || p.queue[0].Kind != "move" || p.queue[0].X != p.cursor.X {
		t.Fatal("cursor did not follow the finger", p.cursor, p.queue)
	}
}

func TestDesktopPageFrameStatuses(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.phase, p.polling = "live", true
	m.receiveDesktopFrame(p, nil, nil, false, &control.HTTPError{Status: 403, Message: "请允许屏幕录制"})
	if p.active || p.phase != "error" || p.title != "Permission required on Mac" || p.polling {
		t.Fatal("permission failure did not stop control", p.phase, p.title)
	}
	p.active, p.phase = true, "live"
	m.receiveDesktopFrame(p, nil, nil, false, &control.HTTPError{Status: 410, Message: "Remote control ended"})
	if p.active || p.phase != "ended" || p.image != nil || p.detail != "Remote control ended" {
		t.Fatal("ended lease kept the page live", p.phase)
	}
	if m.desktopPage != p {
		t.Fatal("ended page should stay until the person leaves it")
	}
}

func desktopScroll(x, y float64, dx, dy int) (in desktop.Input) {
	in.Kind, in.X, in.Y, in.DeltaX, in.DeltaY = "scroll", x, y, dx, dy
	return
}

func TestDesktopPageRendersLiveConnectingAndEnded(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.phase, p.trackpad, p.status = "live", true, "Connected · Full desktop"
	tt := ui.NewTester(m.desktopPageView, 390, 750)
	tt.Frame()
	tt.Frame()
	if tt.HasText("Send") {
		t.Fatal("keyboard should start hidden on the full desktop")
	}
	if err := tt.Click("Keyboard"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, s := range []string{"Remote desktop", "Trackpad", "Send", "Return", "Esc", "More", "Connected · Full desktop"} {
		if !tt.HasText(s) {
			t.Fatal("live page missing", s)
		}
	}
	if err := tt.Click("More"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("⌘") || tt.HasText("Move screen") {
		t.Fatal("more keys did not open")
	}
	if err := tt.Click("Switch control mode, currently Trackpad"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.trackpad {
		t.Fatal("still in trackpad mode")
	}
	if err := tt.Click("More options"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Move screen") {
		t.Fatal("touch mode choices missing")
	}
	m.desktopStage(p, "connecting", "Connecting to Mac", "If your Mac is asleep, its screen will wake first.", true)
	tt.Frame()
	if !tt.HasText("If your Mac is asleep, its screen will wake first.") || tt.HasText("Send") {
		t.Fatal("connecting state")
	}
	m.desktopFinish(p, "Remote control ended")
	tt.Frame()
	if !tt.HasText("Control session ended") || !tt.HasText("Remote control ended") || !tt.HasText("Back to conversation") {
		t.Fatal("ended state")
	}
}

func TestDesktopPageTextInputAllowsInputMethodsUnlessPassword(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.phase = "live"
	tt := ui.NewTester(m.desktopPageView, 390, 750)
	tt.Frame()
	if err := tt.Click("Keyboard"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.secret || !tt.HasText("switch to Password input") || tt.HasText("switch to Text input") {
		t.Fatal("full desktop should start with plain text input")
	}
	if err := tt.Click("Current: Text input, switch to Password input"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !p.secret || !tt.HasText("switch to Text input") {
		t.Fatal("password entry was not selected")
	}
}

func TestDesktopPagePickerOffersFullDesktopAndWindows(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.active, p.image = false, nil
	p.pickable, p.windowOnly = true, true
	m.desktopStage(p, "picker", "Select a window to control", "", false)
	p.windows = []desktop.Window{{ID: 5, PID: 9, App: "Notes", Title: "Draft"}}
	tt := ui.NewTester(m.desktopPageView, 390, 750)
	tt.Frame()
	for _, s := range []string{"Select window", "Full desktop", "Draft", "Notes", "Refresh list"} {
		if !tt.HasText(s) {
			t.Fatal("picker missing", s)
		}
	}
	p.pickable = false
	tt.Frame()
	if tt.HasText("Full desktop") {
		t.Fatal("authorization picker offered the full desktop")
	}
}

func TestDesktopPagePickerCardsShowPreviews(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	thumb := p.image
	p.active, p.image = false, nil
	p.pickable, p.windowOnly = true, true
	m.desktopStage(p, "picker", "Select a window to control", "", false)
	p.windows = []desktop.Window{{ID: 5, PID: 9, App: "Notes", Title: "Draft"}, {ID: 6, PID: 9, App: "Notes", Title: "Ideas"}, {ID: 7, PID: 3, App: "Mail"}}
	p.thumbBusy = true // keep tests off the network
	p.thumbs = map[string]ui.ImageSource{"5-9": thumb, "6-9": nil}
	tt := ui.NewTester(m.desktopPageView, 390, 750)
	tt.Frame()
	for _, s := range []string{"Draft", "Ideas", "Mail", "Notes"} {
		if !tt.HasText(s) {
			t.Fatal("window card missing", s)
		}
	}
	draft, _ := tt.Find("Draft")
	ideas, _ := tt.Find("Ideas")
	mail, _ := tt.Find("Mail")
	if ideas.Y != mail.Y || mail.X <= ideas.X || ideas.Y <= draft.Y {
		t.Fatal("windows are not two to a row", draft, ideas, mail)
	}
}

func TestDesktopPageTapOnRenderedImageReachesViewport(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.phase, p.trackpad = "live", false
	tt := ui.NewTester(m.desktopPageView, 390, 750)
	tt.Frame()
	tt.Frame()
	if p.view.W == 0 {
		t.Fatal("viewport was not measured")
	}
	r, ok := p.imageRect()
	if !ok {
		t.Fatal("no image rect")
	}
	vp, found := tt.Find("Live Mac screen. Tap, swipe, or pinch to zoom.")
	if !found {
		t.Fatal("viewport not found")
	}
	tt.ClickAt(vp.X+r.X+r.W/2, vp.Y+r.Y+r.H/2)
	tt.Frame()
	t.Logf("viewport=%v image=%v queue=%v", vp, r, p.queue)
	if len(p.queue) != 1 || p.queue[0].Kind != "click" || p.queue[0].X < .45 || p.queue[0].X > .55 {
		t.Fatal("tap on the rendered image did not click", p.queue)
	}
}

func TestDesktopPageTapRippleMarksQueuedClicks(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.phase, p.trackpad = "live", false
	r, _ := p.imageRect()
	tap(m, p, r.X-1, r.Y+r.H/2)
	if len(p.taps) != 0 {
		t.Fatal("a tap outside the image showed a ripple")
	}
	tap(m, p, r.X+r.W/2, r.Y+r.H/2)
	if len(p.taps) != 1 || p.taps[0].at.X < .49 || p.taps[0].at.X > .51 {
		t.Fatal("ripple not at the click", p.taps)
	}
	p.touchMode = 1
	m.desktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerDown, X: r.X + 20, Y: r.Y + 20})
	m.desktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerMove, X: r.X + 80, Y: r.Y + 20})
	m.desktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerUp, X: r.X + 80, Y: r.Y + 20})
	if len(p.taps) != 3 {
		t.Fatal("drag did not mark both ends", p.taps)
	}
	tt := ui.NewTester(m.desktopPageView, 390, 750)
	tt.Frame()
	if len(p.activeTaps(time.Now().Add(desktopTapDuration))) != 0 {
		t.Fatal("ripples did not expire")
	}
}

func TestDesktopPageSavedPasswordLivesInMoreOptions(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.phase, p.unlock.saved, p.unlock.key, p.trackpad = "live", true, "mac", true
	p.status = "Connected · Full desktop"
	m.router().Push("/desktop")
	tt := ui.NewTester(m.viewRoot, 390, 750)
	tt.Frame()
	tt.Frame()
	if tt.HasText("Password saved for this Mac") || tt.HasText("Send") {
		t.Fatal("the screen should keep the page until a tool opens")
	}
	saveMobileNativeSnapshot(t, tt, "desktop-live")
	if err := tt.Click("More options"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Password saved for this Mac") || !tt.HasText("Forget password") {
		t.Fatal("saved password missing from more options")
	}
	saveMobileNativeSnapshot(t, tt, "desktop-live-more")
	if err := tt.Click("Keyboard"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.more || !tt.HasText("Send") {
		t.Fatal("keyboard did not open")
	}
	saveMobileNativeSnapshot(t, tt, "desktop-live-keyboard")
}
