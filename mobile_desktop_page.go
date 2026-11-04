package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/desktop"
	"miya/internal/remote"
)

// The remote desktop page is native MyGo UI. Frames and input travel over the
// paired Tailcat files stream with the lease token held in this process only.

type desktopPoint struct{ X, Y float64 }

type desktopPointer struct {
	active                       bool
	startX, startY, lastX, lastY float32
	at                           time.Time
	moved, drag                  bool
	from                         desktopPoint
	seq                          int
}

type mobileDesktopPage struct {
	client   *desktopClient
	conn     *remote.Conn
	released bool
	ctx      context.Context
	cancel   context.CancelFunc
	gen      int

	phase         string // connecting, picker, live, error, ended
	title, detail string
	busy          bool

	windowOnly bool
	window     desktop.Window
	windows    []desktop.Window

	active, polling bool
	image           ui.ImageSource
	imageHash       [32]byte
	status          string

	zoom, panX, panY float32
	view             ui.Rect
	trackpad         bool
	touchMode        int // 0 tap and swipe, 1 drag, 2 move the zoomed view
	cursor           desktopPoint
	pointer          desktopPointer
	lastTap          time.Time
	scrollX, scrollY float32

	text     string
	modifier string
	moreKeys bool
	// secret hides typed text; authorization windows start with it on.
	secret bool
	// pickable leases came from the menu and may choose any window.
	pickable bool
	// thumbs holds picker previews by window; nil marks one that failed.
	thumbs    map[string]ui.ImageSource
	thumbBusy bool

	// taps are recent clicks shown as a fading ripple where they landed.
	taps    []desktopTap
	queue   []desktop.Input
	sending bool

	actions                                  desktopActions
	actionsFresh, actionsBusy, actionPending bool
	actionsUnsupported                       bool
	actionStatus                             string
}

func (m *mobileApp) desktopPrefsFile() string {
	if m.dataDir == "" {
		return ""
	}
	return filepath.Join(m.dataDir, "desktop-control.json")
}

func (m *mobileApp) desktopUIOpen() bool { return m.filesCancel != nil || m.desktopPage != nil }

func (m *mobileApp) openDesktopPage(conn *remote.Conn, token string) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &mobileDesktopPage{client: newDesktopClient(conn.DialFiles, token), conn: conn, ctx: ctx, cancel: cancel, gen: m.generation,
		zoom: 1, cursor: desktopPoint{.5, .5}, trackpad: readDesktopPrefs(m.desktopPrefsFile()).PointerMode != "touch"}
	m.desktopPage = p
	m.router().Push("/desktop")
	m.desktopInitialize(p)
}

// closeDesktopPage revokes the lease and forgets the page. back also leaves
// the page, as when the approval it served has been resolved.
func (m *mobileApp) closeDesktopPage(back bool) {
	p := m.desktopPage
	if p == nil {
		return
	}
	m.desktopPage = nil
	m.desktopApproval = mobileDesktopApproval{}
	m.desktopRelease(p)
	if p.phase != "ended" {
		go p.client.stop()
	}
	p.cancel()
	p.image, p.queue, p.text = nil, nil, ""
	if back && m.router().Path() == "/desktop" {
		m.router().Back()
	}
}

// desktopDo runs work off the main thread and applies its result only while
// the same page is still open and has not ended.
func (m *mobileApp) desktopDo(p *mobileDesktopPage, work func(ctx context.Context) func()) {
	ctx := p.ctx
	go func() {
		apply := work(ctx)
		// A closed page queues nothing more on the main thread.
		if ctx.Err() != nil {
			return
		}
		m.onMain(p.gen, func() {
			if m.desktopPage != p || p.phase == "ended" {
				return
			}
			apply()
			invalidate()
		})
	}()
}

func (m *mobileApp) desktopAfter(p *mobileDesktopPage, d time.Duration, f func()) {
	ctx := p.ctx
	time.AfterFunc(d, func() {
		if ctx.Err() != nil {
			return
		}
		m.onMain(p.gen, func() {
			if m.desktopPage == p && p.phase != "ended" {
				f()
				invalidate()
			}
		})
	})
}

func (m *mobileApp) desktopStage(p *mobileDesktopPage, phase, title, detail string, busy bool) {
	p.phase, p.title, p.detail, p.busy = phase, title, detail, busy
}

func (m *mobileApp) desktopFail(p *mobileDesktopPage, title string, err error) {
	if desktopStatus(err) == 410 {
		m.desktopFinish(p, err.Error())
		return
	}
	p.active = false
	m.desktopStage(p, "error", title, err.Error(), false)
}

// desktopFinish keeps the page with its reason; the lease is already gone
// or is revoked here, and the last frame and any typed text are dropped.
func (m *mobileApp) desktopFinish(p *mobileDesktopPage, message string) {
	if p.phase == "ended" {
		return
	}
	p.active, p.image, p.queue, p.text = false, nil, nil, ""
	m.desktopStage(p, "ended", "控制已结束", message, false)
	p.cancel()
	go p.client.stop()
	m.desktopRelease(p)
	invalidate()
}

func (m *mobileApp) desktopInitialize(p *mobileDesktopPage) {
	m.desktopStage(p, "connecting", "正在连接 Mac", "Mac 休眠时会先唤醒屏幕。", true)
	m.desktopDo(p, func(ctx context.Context) func() {
		s, err := p.client.session(ctx)
		if err != nil {
			return func() { m.desktopFail(p, "连接未完成", err) }
		}
		if s.Pick && s.WindowOnly && s.Window != nil && s.Window.ID == 0 {
			return func() { p.pickable, p.windowOnly = true, true; m.desktopChoose(p) }
		}
		if !s.WindowOnly || s.Window == nil {
			return func() { p.windowOnly, p.pickable = false, s.Pick; m.desktopStart(p) }
		}
		want := *s.Window
		windows, err := p.client.windows(ctx)
		return func() {
			p.windowOnly, p.secret = true, true
			if err != nil {
				m.desktopFail(p, "连接未完成", err)
				return
			}
			p.windows = windows
			for _, w := range windows {
				if w.ID == want.ID && w.PID == want.PID {
					m.desktopSelect(p, w)
					return
				}
			}
			m.desktopFinish(p, "目标授权窗口已关闭。可以返回会话查看结果。")
		}
	})
}

func (m *mobileApp) desktopChoose(p *mobileDesktopPage) {
	// Switching revokes input: the next window needs its own frame first.
	p.active, p.image, p.queue, p.actionsFresh = false, nil, nil, false
	m.desktopStage(p, "picker", "选择操作窗口", "", true)
	m.desktopDo(p, func(ctx context.Context) func() {
		windows, err := p.client.windows(ctx)
		return func() {
			if err != nil {
				m.desktopFail(p, "暂时无法获取窗口", err)
				return
			}
			p.windows, p.busy, p.thumbs = windows, false, nil
			m.desktopLoadThumbs(p)
		}
	})
}

func (m *mobileApp) desktopSelect(p *mobileDesktopPage, w desktop.Window) {
	m.desktopStage(p, "connecting", "正在打开窗口", "正在确认连接和系统权限。", true)
	m.desktopDo(p, func(ctx context.Context) func() {
		err := p.client.selectWindow(ctx, w)
		return func() {
			if err != nil {
				m.desktopFail(p, "暂时无法打开窗口", err)
				return
			}
			p.window, p.windowOnly, p.zoom, p.panX, p.panY = w, true, 1, 0, 0
			m.desktopStart(p)
		}
	})
}

func (m *mobileApp) desktopStart(p *mobileDesktopPage) {
	if p.active {
		return
	}
	title := "正在连接 Mac"
	if p.windowOnly {
		title = "正在打开窗口"
	}
	m.desktopStage(p, "connecting", title, "Mac 休眠时会先唤醒屏幕。", true)
	m.desktopDo(p, func(ctx context.Context) func() {
		err := p.client.start(ctx)
		return func() {
			if err != nil {
				m.desktopFail(p, "暂时无法操作", err)
				return
			}
			p.active, p.status = true, "正在获取画面…"
			m.desktopStage(p, "live", "", "", false)
			m.desktopPoll(p)
			m.desktopRefreshActions(p)
		}
	})
}

func (m *mobileApp) desktopRetry(p *mobileDesktopPage) {
	switch {
	case p.active:
		m.desktopPoll(p)
	case p.windowOnly && p.window.ID != 0:
		m.desktopStart(p)
	default:
		m.desktopInitialize(p)
	}
}

func (p *mobileDesktopPage) connectedStatus() string {
	if p.windowOnly {
		return "已连接 · " + firstNonEmpty(p.window.Title, p.window.App, "授权窗口")
	}
	return "已连接 · 完整桌面"
}

// desktopPoll fetches frames one at a time; a slow frame never blocks input.
func (m *mobileApp) desktopPoll(p *mobileDesktopPage) {
	if !p.active || p.polling {
		return
	}
	p.polling = true
	m.desktopDo(p, func(ctx context.Context) func() {
		data, err := p.client.frame(ctx)
		var image ui.ImageSource
		if err == nil {
			if image, _ = ui.DecodeImage(data); image == nil {
				err = fmt.Errorf("无法解码画面，正在重试")
			}
		}
		return func() { m.receiveDesktopFrame(p, data, image, err) }
	})
}

func (m *mobileApp) receiveDesktopFrame(p *mobileDesktopPage, data []byte, image ui.ImageSource, err error) {
	p.polling = false
	if !p.active {
		return
	}
	delay := 300 * time.Millisecond
	switch status := desktopStatus(err); {
	case err == nil:
		hash := sha256.Sum256(data)
		if p.image == nil || hash != p.imageHash {
			p.image, p.imageHash = image, hash
		}
		p.status = p.connectedStatus()
	case status == 410:
		m.desktopFinish(p, err.Error())
		return
	case status == 403:
		m.desktopFail(p, "需要 Mac 授权", err)
		return
	case status == 429:
		delay = 350 * time.Millisecond
		p.status = "正在等待 Mac 响应…"
	default:
		delay = 1500 * time.Millisecond
		p.status = err.Error()
	}
	m.desktopAfter(p, delay, func() { m.desktopPoll(p) })
}

func (m *mobileApp) desktopRefreshActions(p *mobileDesktopPage) {
	if !p.windowOnly || !p.active || p.actionsBusy || p.actionsUnsupported {
		return
	}
	p.actionsBusy = true
	m.desktopDo(p, func(ctx context.Context) func() {
		a, err := p.client.actions(ctx)
		return func() {
			p.actionsBusy = false
			switch status := desktopStatus(err); {
			case err == nil:
				p.actions, p.actionsFresh = a, true
			case status == 410:
				m.desktopFinish(p, err.Error())
				return
			case status == 404 || status == 403:
				p.actionsUnsupported, p.actionsFresh = true, false
				return
			default:
				p.actionsFresh = false
				p.actionStatus = err.Error()
			}
			m.desktopAfter(p, 2*time.Second, func() { m.desktopRefreshActions(p) })
		}
	})
}

func (m *mobileApp) desktopPressAction(p *mobileDesktopPage, a desktop.Action) {
	if p.actionPending || !p.actionsFresh || !a.Enabled || !p.active {
		return
	}
	p.actionPending, p.actionStatus = true, "正在确认授权结果…"
	w := p.actions.Window
	m.desktopDo(p, func(ctx context.Context) func() {
		err := p.client.pressAction(ctx, a.ID, w)
		return func() {
			p.actionPending, p.actionsFresh = false, false
			if desktopStatus(err) == 410 {
				m.desktopFinish(p, err.Error())
				return
			}
			if err != nil {
				p.actionStatus = "尚未确认授权结果，请查看窗口状态后重试。" + err.Error()
			} else {
				p.actionStatus = "请查看窗口提示；授权尚未完成时可在这里重试。"
			}
			m.desktopRefreshActions(p)
		}
	})
}

// desktopSend keeps input in order. Consecutive moves keep the latest;
// consecutive scrolls add up, so a fast swipe cannot flood the Mac.
func (m *mobileApp) desktopSend(p *mobileDesktopPage, in desktop.Input) {
	if !p.active || p.image == nil {
		return
	}
	if n := len(p.queue); n > 0 {
		last := &p.queue[n-1]
		if in.Kind == "move" && last.Kind == "move" {
			*last = in
			return
		}
		if in.Kind == "scroll" && last.Kind == "scroll" {
			last.DeltaX = desktopClampInt(last.DeltaX + in.DeltaX)
			last.DeltaY = desktopClampInt(last.DeltaY + in.DeltaY)
			last.X, last.Y = in.X, in.Y
			return
		}
		if n >= 64 {
			p.status = "操作过多，请稍候"
			return
		}
	}
	p.queue = append(p.queue, in)
	p.markTap(in, time.Now())
	m.desktopPump(p)
}

func (m *mobileApp) desktopPump(p *mobileDesktopPage) {
	if p.sending || len(p.queue) == 0 || !p.active {
		return
	}
	in := p.queue[0]
	p.queue = p.queue[1:]
	p.sending = true
	m.desktopDo(p, func(ctx context.Context) func() {
		err := p.client.input(ctx, in)
		return func() {
			p.sending = false
			switch {
			case desktopStatus(err) == 410:
				m.desktopFinish(p, err.Error())
				return
			case err != nil && in.Kind != "move" && in.Kind != "scroll":
				p.status = "尚未确认送达，请先查看画面再重试。" + err.Error()
			case err == nil && in.Kind == "text":
				p.status = "已发送到窗口，请在画面中确认。"
			}
			if in.Kind != "move" && in.Kind != "scroll" {
				p.actionsFresh = false
				m.desktopRefreshActions(p)
			}
			m.desktopAfter(p, 30*time.Millisecond, func() { m.desktopPump(p) })
		}
	})
}

func desktopClampInt(v int) int              { return max(-1000, min(1000, v)) }
func desktopClamp(v, lo, hi float32) float32 { return max(lo, min(hi, v)) }
func desktopDist(dx, dy float32) float32 {
	return float32(math.Hypot(float64(dx), float64(dy)))
}

// imageRect is where the frame shows inside the viewport, in local DIPs.
func (p *mobileDesktopPage) imageRect() (ui.Rect, bool) {
	if p.image == nil {
		return ui.Rect{}, false
	}
	iw, ih := ui.ImageSize(p.image)
	vw, vh := p.view.W-16, p.view.H-16
	if iw <= 0 || ih <= 0 || vw <= 0 || vh <= 0 {
		return ui.Rect{}, false
	}
	s := min(vw/iw, vh/ih) * p.zoom
	w, h := iw*s, ih*s
	maxX, maxY := max(0, (w-p.view.W)/2), max(0, (h-p.view.H)/2)
	p.panX, p.panY = desktopClamp(p.panX, -maxX, maxX), desktopClamp(p.panY, -maxY, maxY)
	return ui.Rect{X: (p.view.W-w)/2 + p.panX, Y: (p.view.H-h)/2 + p.panY, W: w, H: h}, true
}

func (p *mobileDesktopPage) point(x, y float32) (desktopPoint, bool) {
	r, ok := p.imageRect()
	if !ok || !r.Contains(x, y) {
		return desktopPoint{}, false
	}
	return desktopPoint{X: float64(desktopClamp((x-r.X)/r.W, 0, 1)), Y: float64(desktopClamp((y-r.Y)/r.H, 0, 1))}, true
}

func (p *mobileDesktopPage) setZoom(z float32) {
	p.zoom = desktopClamp(z, 1, 4)
}

// moveCursor accelerates faster swipes, as the previous page did.
func (p *mobileDesktopPage) moveCursor(dx, dy float32) {
	r, ok := p.imageRect()
	if !ok {
		return
	}
	gain := 1.1 + min(1.4, desktopDist(dx, dy)/24)
	p.cursor.X = float64(desktopClamp(float32(p.cursor.X)+dx*gain/r.W, 0, 1))
	p.cursor.Y = float64(desktopClamp(float32(p.cursor.Y)+dy*gain/r.H, 0, 1))
	// Keep the cursor on screen when zoomed in.
	cx, cy := r.X+float32(p.cursor.X)*r.W, r.Y+float32(p.cursor.Y)*r.H
	const margin = 24
	if cx < margin {
		p.panX += margin - cx
	} else if cx > p.view.W-margin {
		p.panX -= cx - (p.view.W - margin)
	}
	if cy < margin {
		p.panY += margin - cy
	} else if cy > p.view.H-margin {
		p.panY -= cy - (p.view.H - margin)
	}
}

func (m *mobileApp) desktopAtCursor(p *mobileDesktopPage, kind string) {
	m.desktopSend(p, desktop.Input{Kind: kind, X: p.cursor.X, Y: p.cursor.Y})
}

func (m *mobileApp) desktopPointerInput(p *mobileDesktopPage, ev ui.InputEvent) bool {
	if !p.active || p.image == nil {
		return false
	}
	g := &p.pointer
	switch ev.Kind {
	case ui.InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		*g = desktopPointer{active: true, startX: ev.X, startY: ev.Y, lastX: ev.X, lastY: ev.Y, at: time.Now(), from: p.cursor, seq: g.seq + 1}
		if p.trackpad {
			seq := g.seq
			m.desktopAfter(p, 450*time.Millisecond, func() {
				if p.pointer.active && p.pointer.seq == seq && !p.pointer.moved {
					p.pointer.drag = true
				}
			})
		}
		return true
	case ui.InputPointerMove:
		if !g.active {
			return false
		}
		if !g.moved {
			if desktopDist(ev.X-g.startX, ev.Y-g.startY) < 8 {
				return true
			}
			g.moved = true
		}
		dx, dy := ev.X-g.lastX, ev.Y-g.lastY
		g.lastX, g.lastY = ev.X, ev.Y
		if p.trackpad {
			p.moveCursor(dx, dy)
			m.desktopAtCursor(p, "move")
		} else if p.touchMode == 2 {
			p.panX, p.panY = p.panX+dx, p.panY+dy
		}
		return true
	case ui.InputPointerUp:
		if !g.active {
			return false
		}
		down := *g
		g.active = false
		if p.trackpad {
			if down.drag {
				if down.moved {
					m.desktopSend(p, desktop.Input{Kind: "drag", X: down.from.X, Y: down.from.Y, ToX: p.cursor.X, ToY: p.cursor.Y})
				}
				return true
			}
			if !down.moved && time.Since(down.at) < 400*time.Millisecond {
				kind := "click"
				if time.Since(p.lastTap) < 350*time.Millisecond {
					kind, p.lastTap = "doubleClick", time.Time{}
				} else {
					p.lastTap = time.Now()
				}
				m.desktopAtCursor(p, kind)
			}
			return true
		}
		start, startOK := p.point(down.startX, down.startY)
		end, endOK := p.point(ev.X, ev.Y)
		dx, dy := ev.X-down.startX, ev.Y-down.startY
		switch {
		case p.touchMode == 2:
		case p.touchMode == 1:
			if startOK && endOK {
				m.desktopSend(p, desktop.Input{Kind: "drag", X: start.X, Y: start.Y, ToX: end.X, ToY: end.Y})
			}
		case desktopDist(dx, dy) > 10:
			if startOK {
				m.desktopSend(p, desktop.Input{Kind: "scroll", X: start.X, Y: start.Y, DeltaX: desktopClampInt(int(math.Round(float64(dx * 2)))), DeltaY: desktopClampInt(int(math.Round(float64(dy * 2))))})
			}
		case endOK:
			m.desktopSend(p, desktop.Input{Kind: "click", X: end.X, Y: end.Y})
		}
		return true
	case ui.InputPointerCancel:
		g.active = false
		return true
	case ui.InputZoom:
		if ev.Scale > 0 {
			p.setZoom(p.zoom * ev.Scale)
		}
		return true
	}
	return false
}

func (m *mobileApp) desktopGestures(p *mobileDesktopPage, events []ui.GestureEvent) {
	for _, e := range events {
		switch e.Kind {
		case "pinch":
			if e.Scale > 0 {
				p.setZoom(p.zoom * e.Scale)
			}
		case "pan":
			if p.trackpad && p.active {
				p.scrollX, p.scrollY = p.scrollX+e.DX*2, p.scrollY+e.DY*2
				dx, dy := int(p.scrollX), int(p.scrollY)
				if dx != 0 || dy != 0 {
					p.scrollX, p.scrollY = p.scrollX-float32(dx), p.scrollY-float32(dy)
					m.desktopSend(p, desktop.Input{Kind: "scroll", X: p.cursor.X, Y: p.cursor.Y, DeltaX: dx, DeltaY: dy})
				}
			} else {
				p.panX, p.panY = p.panX+e.DX, p.panY+e.DY
			}
		case "tap":
			if e.Phase == "end" && p.trackpad {
				m.desktopAtCursor(p, "rightClick")
			}
		}
	}
}

func (m *mobileApp) desktopKey(p *mobileDesktopPage, key string) {
	m.desktopSend(p, desktop.Input{Kind: "key", Key: key, Modifier: p.modifier})
	p.modifier = ""
}

func (m *mobileApp) desktopSendText(p *mobileDesktopPage) {
	text := p.text
	if text == "" || !p.active {
		return
	}
	if len(text) > 4096 {
		p.status = "每次最多发送 4096 字节文字"
		return
	}
	p.text = ""
	m.desktopSend(p, desktop.Input{Kind: "text", Text: text})
}

func (m *mobileApp) desktopPageView(c *ui.Context) {
	p := m.desktopPage
	pal := m.pal
	if p == nil {
		ui.Column(c).Fill().Background(pal.bg)
		return
	}
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).FillWidth().MinHeight(56).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			m.navigationBack(c, "/desktop")
			title := "远程桌面"
			if p.windowOnly {
				title = firstNonEmpty(p.window.Title, p.window.App, "授权窗口")
			}
			if p.phase == "picker" {
				title = "选择窗口"
			}
			ui.Text(c, title).Grow(1).MinWidth(0).TextAlign(ui.Center).FontSize(m.font(15)).FontWeight(600).MaxLines(1)
			if p.phase == "live" {
				label := "触控板"
				if !p.trackpad {
					label = "触控"
				}
				if m.pill(c, "desktop-pointer", label, false).Label("切换操作方式，当前" + label).Clicked() {
					p.trackpad, p.pointer = !p.trackpad, desktopPointer{}
					mode := map[bool]string{true: "trackpad", false: "touch"}[p.trackpad]
					path := m.desktopPrefsFile()
					go func() { _ = writeDesktopPrefs(path, mode) }()
				}
			}
			if (p.windowOnly || p.pickable) && (p.phase == "live" || p.phase == "error") {
				if m.pill(c, "desktop-choose", "切换窗口", false).Clicked() {
					m.desktopChoose(p)
				}
			}
		})
		switch p.phase {
		case "live":
			m.desktopLiveView(c, p)
		case "picker":
			m.desktopPickerView(c, p)
		default:
			m.desktopStateView(c, p)
		}
	})
}

func (m *mobileApp) desktopStateView(c *ui.Context, p *mobileDesktopPage) {
	pal := m.pal
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Padding(24).Gap(14).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		if p.busy {
			ui.Spinner(c)
		}
		ui.Text(c, p.title).FontSize(m.font(20)).FontWeight(600).TextAlign(ui.Center)
		if p.detail != "" {
			ui.Text(c, p.detail).FontSize(m.font(15)).TextColor(pal.muted).TextAlign(ui.Center).LineHeight(1.5)
		}
		if p.phase == "error" {
			if m.pill(c, "desktop-retry", "重新连接", true).Clicked() {
				m.desktopRetry(p)
			}
		}
		if p.phase == "error" || p.phase == "ended" {
			if m.pill(c, "desktop-return", "返回会话", p.phase == "ended").Clicked() {
				m.router().Back()
			}
		}
	})
}

func (m *mobileApp) desktopPickerView(c *ui.Context, p *mobileDesktopPage) {
	pal := m.pal
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(16).Gap(10).Children(func() {
			ui.Text(c, "仅共享选中的窗口。系统要求 Touch ID 或本机确认时，请在 Mac 上完成。").FontSize(m.font(14)).TextColor(pal.muted).LineHeight(1.5)
			if p.pickable && !p.busy {
				if m.pill(c, "desktop-full", "完整桌面", false).FillWidth().Clicked() {
					m.desktopSelectFull(p)
				}
			}
			if p.busy {
				ui.Spinner(c).Margin(12)
				return
			}
			if len(p.windows) == 0 {
				ui.Text(c, "没有可共享的窗口。").FontSize(m.font(15)).TextColor(pal.muted)
			}
			m.desktopWindowGrid(c, p)
			if m.pill(c, "desktop-refresh-windows", "刷新列表", false).Clicked() {
				m.desktopChoose(p)
			}
		})
	})
}

var desktopKeys = []struct{ key, label string }{{"enter", "回车"}, {"tab", "Tab"}, {"backspace", "退格"}, {"escape", "Esc"}}
var desktopMoreKeys = []struct{ key, label string }{{"left", "←"}, {"right", "→"}, {"up", "↑"}, {"down", "↓"}, {"a", "A"}, {"c", "C"}, {"v", "V"}, {"z", "Z"}, {"l", "L"}}
var desktopModifiers = []struct{ key, label string }{{"command", "⌘"}, {"control", "⌃"}, {"shift", "⇧"}, {"option", "⌥"}}

func (m *mobileApp) desktopLiveView(c *ui.Context, p *mobileDesktopPage) {
	pal := m.pal
	vp := ui.Column(c.Key("desktop-viewport")).FillWidth().Grow(1).MinHeight(0).Clip().Background(pal.surface).Label("Mac 实时画面，可轻点、滑动或双指缩放")
	if b := vp.Bounds(); b.W > 0 && b.H > 0 {
		p.view = ui.Rect{W: b.W, H: b.H}
	}
	m.desktopGestures(p, vp.Gestures())
	vp.HandleInput(func(ev ui.InputEvent) bool { return m.desktopPointerInput(p, ev) })
	r, haveImage := p.imageRect()
	taps := p.activeTaps(time.Now())
	if len(taps) > 0 {
		c.After(16 * time.Millisecond) // animate until the ripples fade
	}
	if haveImage && (p.trackpad || len(taps) > 0) {
		trackpad, cursor, dragging := p.trackpad, p.cursor, p.pointer.active && p.pointer.drag
		vp.DrawOver(func(painter *ui.Painter, box ui.Rect) {
			now := time.Now()
			for _, tap := range taps {
				t := float32(now.Sub(tap.when)) / float32(desktopTapDuration)
				if t < 0 || t >= 1 {
					continue
				}
				x, y := box.X+r.X+float32(tap.at.X)*r.W, box.Y+r.Y+float32(tap.at.Y)*r.H
				radius := 10 + 22*t
				painter.FillPath(new(ui.Path).Circle(x, y, radius), ui.RGBA(10, 132, 255, 0.25*(1-t)))
				painter.StrokePath(new(ui.Path).Circle(x, y, radius), 2, ui.RGBA(10, 132, 255, 0.9*(1-t)))
			}
			if !trackpad {
				return
			}
			x, y := box.X+r.X+float32(cursor.X)*r.W, box.Y+r.Y+float32(cursor.Y)*r.H
			radius := float32(7)
			if dragging {
				radius = 11
			}
			painter.FillPath(new(ui.Path).Circle(x, y, radius), pal.ink)
			painter.StrokePath(new(ui.Path).Circle(x, y, radius), 2, pal.bg)
		})
	}
	vp.Children(func() {
		if haveImage {
			ui.Image(c, p.image).Absolute().Left(r.X).Top(r.Y).Width(r.W).Height(r.H).Fit(ui.FillBox)
			return
		}
		ui.Column(c).Fill().Gap(10).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Spinner(c)
			ui.Text(c, p.status).FontSize(m.font(14)).TextColor(pal.muted).TextAlign(ui.Center).MaxLines(3)
		})
	})
	ui.Column(c).FillWidth().Padding(8, 12, 10, 12).Gap(8).Children(func() {
		ui.Text(c, p.status).FontSize(m.font(12)).TextColor(pal.muted).MaxLines(1)
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			// A secure field forces iOS's ASCII keyboard without input methods,
			// so hide the text only when the person chooses password entry.
			mode, other := "文字", "密码"
			if p.secret {
				mode, other = other, mode
			}
			if m.pill(c, "desktop-secret", mode, false).Label("当前" + mode + "输入，切换为" + other + "输入").Clicked() {
				p.secret = !p.secret
			}
			field := ui.TextInput(c.Key("desktop-text"), &p.text).Grow(1).MinWidth(0).MinHeight(46).Padding(12).Background(pal.surface).Radius(12).
				InputOptions(ui.InputOptions{Correction: ui.CorrectionOff, Capitalization: ui.CapitalizeNone, Return: ui.ReturnGo})
			if p.secret {
				field.Password().Label("发送到 Mac 的密码").Placeholder("输入密码")
			} else {
				field.Label("发送到 Mac 的文字").Placeholder("输入要发送的文字")
			}
			if m.pill(c, "desktop-send", "发送", true).Disabled(p.text == "").Clicked() {
				m.desktopSendText(p)
			}
		})
		ui.Row(c).FillWidth().Wrap().Gap(6).Children(func() {
			for _, k := range desktopKeys {
				if m.pill(c, "desktop-key-"+k.key, k.label, false).Clicked() {
					m.desktopKey(p, k.key)
				}
			}
			label := "更多"
			if p.moreKeys {
				label = "收起"
			}
			if m.pill(c, "desktop-more", label, false).Clicked() {
				p.moreKeys = !p.moreKeys
			}
		})
		if p.moreKeys {
			ui.Row(c).FillWidth().Wrap().Gap(6).Children(func() {
				for _, k := range desktopModifiers {
					if m.pill(c, "desktop-mod-"+k.key, k.label, p.modifier == k.key).Clicked() {
						if p.modifier == k.key {
							p.modifier = ""
						} else {
							p.modifier = k.key
						}
					}
				}
				for _, k := range desktopMoreKeys {
					if m.pill(c, "desktop-key-"+k.key, k.label, false).Clicked() {
						m.desktopKey(p, k.key)
					}
				}
			})
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				if m.pill(c, "desktop-zoom-out", "缩小", false).Disabled(p.zoom <= 1).Clicked() {
					p.setZoom(p.zoom / 1.25)
				}
				ui.Text(c, fmt.Sprintf("%d%%", int(math.Round(float64(p.zoom*100))))).FontSize(m.font(13)).TextColor(pal.muted).MinWidth(48).TextAlign(ui.Center)
				if m.pill(c, "desktop-zoom-in", "放大", false).Disabled(p.zoom >= 4).Clicked() {
					p.setZoom(p.zoom * 1.25)
				}
			})
			if !p.trackpad {
				ui.Segmented(c.Key("desktop-touch-mode"), &p.touchMode, "点击·滚动", "拖动", "移动画面").FillWidth()
			}
		}
		if p.windowOnly && !p.actionsUnsupported && len(p.actions.Actions) > 0 {
			ui.Row(c).FillWidth().Wrap().Justify(ui.End).Gap(8).Children(func() {
				for _, a := range p.actions.Actions {
					primary := a.Default && !a.Cancel
					if m.approvalButton(c, "desktop-action-"+a.ID, a.Label, primary, !a.Enabled || !p.actionsFresh || p.actionPending).Clicked() {
						m.desktopPressAction(p, a)
					}
				}
			})
		}
		if p.actionStatus != "" {
			ui.Text(c, p.actionStatus).FontSize(m.font(12)).TextColor(pal.muted).MaxLines(2)
		}
	})
}

// desktopSelectFull switches a menu lease to the full desktop.
func (m *mobileApp) desktopSelectFull(p *mobileDesktopPage) {
	if !p.pickable {
		return
	}
	m.desktopStage(p, "connecting", "正在连接 Mac", "正在切换到完整桌面。", true)
	m.desktopDo(p, func(ctx context.Context) func() {
		err := p.client.selectWindow(ctx, desktop.Window{})
		return func() {
			if err != nil {
				m.desktopFail(p, "暂时无法切换", err)
				return
			}
			p.window, p.windowOnly, p.zoom, p.panX, p.panY = desktop.Window{}, false, 1, 0, 0
			p.actions, p.actionsFresh = desktopActions{}, false
			m.desktopStart(p)
		}
	})
}

var sendDesktopClose = func(conn *remote.Conn, token string) {
	go func() { _ = conn.Send(remote.Request{Op: "desktop-close", DesktopToken: token}) }()
}

// desktopRelease also revokes the lease on the control connection, besides
// /stop on the files stream. A reopen first resends these closes on the same
// connection, so the Mac never handles the new open while the old lease lives.
func (m *mobileApp) desktopRelease(p *mobileDesktopPage) {
	if p.released || p.conn == nil {
		return
	}
	p.released = true
	if p.conn == m.conn {
		m.desktopClosing = append(m.desktopClosing, p.client.token)
	}
	sendDesktopClose(p.conn, p.client.token)
}

func desktopWindowKey(w desktop.Window) string { return fmt.Sprintf("%d-%d", w.ID, w.PID) }

// desktopLoadThumbs fetches picker previews one at a time.
func (m *mobileApp) desktopLoadThumbs(p *mobileDesktopPage) {
	if !p.pickable || p.thumbBusy || p.phase != "picker" {
		return
	}
	for _, w := range p.windows {
		key := desktopWindowKey(w)
		if _, done := p.thumbs[key]; done {
			continue
		}
		p.thumbBusy = true
		m.desktopDo(p, func(ctx context.Context) func() {
			data, err := p.client.thumbnail(ctx, w)
			var img ui.ImageSource
			if err == nil {
				img, _ = ui.DecodeImage(data)
			}
			return func() {
				p.thumbBusy = false
				if desktopStatus(err) == 410 {
					m.desktopFinish(p, err.Error())
					return
				}
				if p.thumbs == nil {
					p.thumbs = map[string]ui.ImageSource{}
				}
				p.thumbs[key] = img
				m.desktopLoadThumbs(p)
			}
		})
		return
	}
}

// desktopWindowGrid shows the shareable windows two to a row.
func (m *mobileApp) desktopWindowGrid(c *ui.Context, p *mobileDesktopPage) {
	for i := 0; i < len(p.windows); i += 2 {
		row := p.windows[i:min(i+2, len(p.windows))]
		ui.Row(c.Key(fmt.Sprintf("desktop-window-row-%d", i))).FillWidth().Gap(10).AlignItems(ui.Start).Children(func() {
			for _, w := range row {
				m.desktopWindowCard(c, p, w)
			}
			if len(row) == 1 {
				ui.Column(c).Grow(1).Basis(0).MinWidth(0)
			}
		})
	}
}

func (m *mobileApp) desktopWindowCard(c *ui.Context, p *mobileDesktopPage, w desktop.Window) {
	pal := m.pal
	key := desktopWindowKey(w)
	img, loaded := p.thumbs[key]
	title := firstNonEmpty(w.Title, w.App, "未命名窗口")
	card := m.phoneButton(c.Key("desktop-window-" + key)).Column().Grow(1).Basis(0).MinWidth(0).Gap(6).Padding(8).Background(pal.surface).Radius(12)
	card.Children(func() {
		ui.Column(c).FillWidth().AspectRatio(1.6).Radius(8).Clip().Background(pal.bg).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			switch {
			case img != nil:
				ui.Image(c, img).Fill().Fit(ui.Contain)
			case !loaded && p.pickable:
				ui.Spinner(c)
			default:
				ui.Text(c, firstNonEmpty(w.App, "窗口")).FontSize(m.font(12)).TextColor(pal.muted).MaxLines(1)
			}
		})
		ui.Text(c, title).FontSize(m.font(14)).FontWeight(600).LineHeight(1.3).MaxLines(2)
		if w.Title != "" && w.App != "" {
			ui.Text(c, w.App).FontSize(m.font(12)).TextColor(pal.muted).MaxLines(1)
		}
	})
	if card.Clicked() {
		m.desktopSelect(p, w)
	}
}

type desktopTap struct {
	at   desktopPoint
	when time.Time
}

const desktopTapDuration = 450 * time.Millisecond

// markTap remembers where a queued click or drag landed for its ripple.
func (p *mobileDesktopPage) markTap(in desktop.Input, now time.Time) {
	switch in.Kind {
	case "click", "doubleClick", "rightClick":
		p.taps = append(p.activeTaps(now), desktopTap{desktopPoint{in.X, in.Y}, now})
	case "drag":
		p.taps = append(p.activeTaps(now), desktopTap{desktopPoint{in.X, in.Y}, now}, desktopTap{desktopPoint{in.ToX, in.ToY}, now})
	default:
		return
	}
	if n := len(p.taps); n > 4 {
		p.taps = p.taps[n-4:]
	}
}

func (p *mobileDesktopPage) activeTaps(now time.Time) []desktopTap {
	keep := p.taps[:0]
	for _, tap := range p.taps {
		if now.Sub(tap.when) < desktopTapDuration {
			keep = append(keep, tap)
		}
	}
	p.taps = keep
	return keep
}
