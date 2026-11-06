package main

import (
	desktop "miya/internal/desktop"
	control "miya/internal/desktop/control"
	mobile "miya/internal/mobile"
	testing "testing"
	time "time"

	ui "github.com/egoist/mygo/ui"
)

// liveTestDesktopPage is a live page whose queue is held, so tests can read
// exactly what would be sent to the Mac.
func liveTestDesktopPage(t *testing.T, m *mobile.App) *mobile.MobileDesktopPage {
	t.Helper()
	img, err := ui.DecodeImage(windowJPEG(t))
	if err != nil {
		t.Fatal(err)
	}
	closed := 0
	openTestDesktopPage(m, &closed)
	p := m.DesktopPage
	// Stop pending hold and refresh timers before other tests swap runOnMain.
	t.Cleanup(p.Cancel)
	p.Active, p.Image, p.View, p.Zoom, p.Cursor, p.Sending = true, img, ui.Rect{W: 400, H: 300}, 1, mobile.DesktopPoint{.5, .5}, true
	return p
}

func tap(m *mobile.App, p *mobile.MobileDesktopPage, x, y float32) {
	m.DesktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerDown, X: x, Y: y})
	m.DesktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerUp, X: x, Y: y})
}

func TestDesktopPageQueueCoalescesMovesAndScrolls(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	m.DesktopAtCursor(p, "move")
	p.Cursor = mobile.DesktopPoint{.7, .2}
	m.DesktopAtCursor(p, "move")
	m.DesktopSend(p, desktopScroll(.1, .1, 30, -40))
	m.DesktopSend(p, desktopScroll(.2, .3, 990, -5))
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

func TestDesktopPageTouchModes(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.Trackpad = false
	r, ok := p.ImageRect()
	if !ok {
		t.Fatal("no image rect")
	}
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	tap(m, p, cx, cy)
	if len(p.Queue) != 1 || p.Queue[0].Kind != "click" || p.Queue[0].X < .49 || p.Queue[0].X > .51 || p.Queue[0].Y < .49 || p.Queue[0].Y > .51 {
		t.Fatal("tap did not click the image point", p.Queue)
	}
	tap(m, p, r.X-1, cy)
	if len(p.Queue) != 1 {
		t.Fatal("tap outside the image was sent")
	}
	p.Queue = nil
	m.DesktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerDown, X: cx, Y: cy})
	m.DesktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerMove, X: cx + 30, Y: cy - 20})
	m.DesktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerUp, X: cx + 30, Y: cy - 20})
	if len(p.Queue) != 1 || p.Queue[0].Kind != "scroll" || p.Queue[0].DeltaX != 60 || p.Queue[0].DeltaY != -40 {
		t.Fatal("swipe did not scroll", p.Queue)
	}
	p.Queue, p.TouchMode = nil, 1
	m.DesktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerDown, X: cx, Y: cy})
	m.DesktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerMove, X: cx + 30, Y: cy})
	m.DesktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerUp, X: cx + 30, Y: cy})
	if len(p.Queue) != 1 || p.Queue[0].Kind != "drag" || p.Queue[0].ToX <= p.Queue[0].X {
		t.Fatal("drag mode did not drag", p.Queue)
	}
	p.Queue, p.TouchMode = nil, 2
	p.SetZoom(2)
	m.DesktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerDown, X: cx, Y: cy})
	m.DesktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerMove, X: cx + 30, Y: cy})
	m.DesktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerUp, X: cx + 30, Y: cy})
	if len(p.Queue) != 0 || p.PanX == 0 {
		t.Fatal("pan mode sent input or did not move the view", p.Queue, p.PanX)
	}
}

func TestDesktopPageTrackpadTapsAndCursor(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.Trackpad = true
	tap(m, p, 10, 10)
	tap(m, p, 300, 250)
	if len(p.Queue) != 2 || p.Queue[0].Kind != "click" || p.Queue[1].Kind != "doubleClick" || p.Queue[0].X != .5 || p.Queue[0].Y != .5 {
		t.Fatal("trackpad taps did not click at the cursor", p.Queue)
	}
	p.Queue = nil
	m.DesktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerDown, X: 100, Y: 100})
	m.DesktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerMove, X: 140, Y: 100})
	m.DesktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerUp, X: 140, Y: 100})
	if p.Cursor.X <= .5 || len(p.Queue) != 1 || p.Queue[0].Kind != "move" || p.Queue[0].X != p.Cursor.X {
		t.Fatal("cursor did not follow the finger", p.Cursor, p.Queue)
	}
}

func TestDesktopPageFrameStatuses(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
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

func desktopScroll(x, y float64, dx, dy int) (in desktop.Input) {
	in.Kind, in.X, in.Y, in.DeltaX, in.DeltaY = "scroll", x, y, dx, dy
	return
}

func TestDesktopPageRendersLiveConnectingAndEnded(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.Phase, p.Trackpad, p.Status = "live", true, "Connected · Full desktop"
	tt := ui.NewTester(m.DesktopPageView, 390, 750)
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
	if p.Trackpad {
		t.Fatal("still in trackpad mode")
	}
	if err := tt.Click("More options"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Move screen") {
		t.Fatal("touch mode choices missing")
	}
	m.DesktopStage(p, "connecting", "Connecting to Mac", "If your Mac is asleep, its screen will wake first.", true)
	tt.Frame()
	if !tt.HasText("If your Mac is asleep, its screen will wake first.") || tt.HasText("Send") {
		t.Fatal("connecting state")
	}
	m.DesktopFinish(p, "Remote control ended")
	tt.Frame()
	if !tt.HasText("Control session ended") || !tt.HasText("Remote control ended") || !tt.HasText("Back to conversation") {
		t.Fatal("ended state")
	}
}

func TestDesktopPageTextInputAllowsInputMethodsUnlessPassword(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.Phase = "live"
	tt := ui.NewTester(m.DesktopPageView, 390, 750)
	tt.Frame()
	if err := tt.Click("Keyboard"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.Secret || !tt.HasText("switch to Password input") || tt.HasText("switch to Text input") {
		t.Fatal("full desktop should start with plain text input")
	}
	if err := tt.Click("Current: Text input, switch to Password input"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !p.Secret || !tt.HasText("switch to Text input") {
		t.Fatal("password entry was not selected")
	}
}

func TestDesktopPagePickerOffersFullDesktopAndWindows(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.Active, p.Image = false, nil
	p.Pickable, p.WindowOnly = true, true
	m.DesktopStage(p, "picker", "Select a window to control", "", false)
	p.Windows = []desktop.Window{{ID: 5, PID: 9, App: "Notes", Title: "Draft"}}
	tt := ui.NewTester(m.DesktopPageView, 390, 750)
	tt.Frame()
	for _, s := range []string{"Select window", "Full desktop", "Draft", "Notes", "Refresh list"} {
		if !tt.HasText(s) {
			t.Fatal("picker missing", s)
		}
	}
	p.Pickable = false
	tt.Frame()
	if tt.HasText("Full desktop") {
		t.Fatal("authorization picker offered the full desktop")
	}
}

func TestDesktopPagePickerCardsShowPreviews(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	thumb := p.Image
	p.Active, p.Image = false, nil
	p.Pickable, p.WindowOnly = true, true
	m.DesktopStage(p, "picker", "Select a window to control", "", false)
	p.Windows = []desktop.Window{{ID: 5, PID: 9, App: "Notes", Title: "Draft"}, {ID: 6, PID: 9, App: "Notes", Title: "Ideas"}, {ID: 7, PID: 3, App: "Mail"}}
	p.ThumbBusy = true // keep tests off the network
	p.Thumbs = map[string]ui.ImageSource{"5-9": thumb, "6-9": nil}
	tt := ui.NewTester(m.DesktopPageView, 390, 750)
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
	p.Phase, p.Trackpad = "live", false
	tt := ui.NewTester(m.DesktopPageView, 390, 750)
	tt.Frame()
	tt.Frame()
	if p.View.W == 0 {
		t.Fatal("viewport was not measured")
	}
	r, ok := p.ImageRect()
	if !ok {
		t.Fatal("no image rect")
	}
	vp, found := tt.Find("Live Mac screen. Tap, swipe, or pinch to zoom.")
	if !found {
		t.Fatal("viewport not found")
	}
	tt.ClickAt(vp.X+r.X+r.W/2, vp.Y+r.Y+r.H/2)
	tt.Frame()
	t.Logf("viewport=%v image=%v queue=%v", vp, r, p.Queue)
	if len(p.Queue) != 1 || p.Queue[0].Kind != "click" || p.Queue[0].X < .45 || p.Queue[0].X > .55 {
		t.Fatal("tap on the rendered image did not click", p.Queue)
	}
}

func TestDesktopPageTapRippleMarksQueuedClicks(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.Phase, p.Trackpad = "live", false
	r, _ := p.ImageRect()
	tap(m, p, r.X-1, r.Y+r.H/2)
	if len(p.Taps) != 0 {
		t.Fatal("a tap outside the image showed a ripple")
	}
	tap(m, p, r.X+r.W/2, r.Y+r.H/2)
	if len(p.Taps) != 1 || p.Taps[0].At.X < .49 || p.Taps[0].At.X > .51 {
		t.Fatal("ripple not at the click", p.Taps)
	}
	p.TouchMode = 1
	m.DesktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerDown, X: r.X + 20, Y: r.Y + 20})
	m.DesktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerMove, X: r.X + 80, Y: r.Y + 20})
	m.DesktopPointerInput(p, ui.InputEvent{Kind: ui.InputPointerUp, X: r.X + 80, Y: r.Y + 20})
	if len(p.Taps) != 3 {
		t.Fatal("drag did not mark both ends", p.Taps)
	}
	tt := ui.NewTester(m.DesktopPageView, 390, 750)
	tt.Frame()
	if len(p.ActiveTaps(time.Now().Add(mobile.DesktopTapDuration))) != 0 {
		t.Fatal("ripples did not expire")
	}
}

func TestDesktopPageSavedPasswordLivesInMoreOptions(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.Phase, p.Unlock.Saved, p.Unlock.Key, p.Trackpad = "live", true, "mac", true
	p.Status = "Connected · Full desktop"
	m.Router().Push("/desktop")
	tt := ui.NewTester(m.ViewRoot, 390, 750)
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
	if p.More || !tt.HasText("Send") {
		t.Fatal("keyboard did not open")
	}
	saveMobileNativeSnapshot(t, tt, "desktop-live-keyboard")
}
