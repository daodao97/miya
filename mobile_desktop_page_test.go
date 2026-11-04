package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/desktop"
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
	m.receiveDesktopFrame(p, nil, nil, &desktopHTTPError{Status: 403, Message: "请允许屏幕录制"})
	if p.active || p.phase != "error" || p.title != "需要 Mac 授权" || p.polling {
		t.Fatal("permission failure did not stop control", p.phase, p.title)
	}
	p.active, p.phase = true, "live"
	m.receiveDesktopFrame(p, nil, nil, &desktopHTTPError{Status: 410, Message: "远程控制已结束"})
	if p.active || p.phase != "ended" || p.image != nil || p.detail != "远程控制已结束" {
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
	p.phase, p.trackpad, p.status = "live", true, "已连接 · 完整桌面"
	tt := ui.NewTester(m.desktopPageView, 390, 750)
	tt.Frame()
	tt.Frame()
	for _, s := range []string{"远程桌面", "触控板", "发送", "回车", "Esc", "更多", "已连接 · 完整桌面"} {
		if !tt.HasText(s) {
			t.Fatal("live page missing", s)
		}
	}
	if err := tt.Click("更多"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("缩小") || !tt.HasText("⌘") || !tt.HasText("100%") || tt.HasText("移动画面") {
		t.Fatal("more keys did not open")
	}
	if err := tt.Click("触控板"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.trackpad || !tt.HasText("移动画面") {
		t.Fatal("touch mode choices missing")
	}
	m.desktopStage(p, "connecting", "正在连接 Mac", "Mac 休眠时会先唤醒屏幕。", true)
	tt.Frame()
	if !tt.HasText("Mac 休眠时会先唤醒屏幕。") || tt.HasText("发送") {
		t.Fatal("connecting state")
	}
	m.desktopFinish(p, "远程控制已结束")
	tt.Frame()
	if !tt.HasText("控制已结束") || !tt.HasText("远程控制已结束") || !tt.HasText("返回会话") {
		t.Fatal("ended state")
	}
}

func TestDesktopPageTextInputAllowsInputMethodsUnlessPassword(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.phase = "live"
	tt := ui.NewTester(m.desktopPageView, 390, 750)
	tt.Frame()
	if p.secret || !tt.HasText("切换为密码输入") || tt.HasText("切换为文字输入") {
		t.Fatal("full desktop should start with plain text input")
	}
	if err := tt.Click("文字"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !p.secret || !tt.HasText("切换为文字输入") {
		t.Fatal("password entry was not selected")
	}
}

func TestDesktopPagePickerOffersFullDesktopAndWindows(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.active, p.image = false, nil
	p.pickable, p.windowOnly = true, true
	m.desktopStage(p, "picker", "选择操作窗口", "", false)
	p.windows = []desktop.Window{{ID: 5, PID: 9, App: "Notes", Title: "Draft"}}
	tt := ui.NewTester(m.desktopPageView, 390, 750)
	tt.Frame()
	for _, s := range []string{"选择窗口", "完整桌面", "Draft", "Notes", "刷新列表"} {
		if !tt.HasText(s) {
			t.Fatal("picker missing", s)
		}
	}
	p.pickable = false
	tt.Frame()
	if tt.HasText("完整桌面") {
		t.Fatal("authorization picker offered the full desktop")
	}
}

func TestDesktopPagePickerCardsShowPreviews(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	thumb := p.image
	p.active, p.image = false, nil
	p.pickable, p.windowOnly = true, true
	m.desktopStage(p, "picker", "选择操作窗口", "", false)
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
	if draft.Y != ideas.Y || ideas.X <= draft.X || mail.Y <= draft.Y {
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
	vp, found := tt.Find("Mac 实时画面，可轻点、滑动或双指缩放")
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
