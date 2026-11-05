package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"miya/internal/desktop"
	"miya/internal/remote"
	"miya/internal/textutil"
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
	// keyboard shows the text field and keys; otherwise the screen gets the room.
	keyboard bool
	// focusText raises the system keyboard once; more opens the options panel.
	focusText, more bool
	// overlays are the floating controls over the screen, in viewport DIPs;
	// touches there never reach the Mac.
	overlays []ui.Rect
	// secret hides typed text; authorization windows start with it on.
	secret bool
	// pickable leases came from the menu and may choose any window.
	pickable             bool
	appsAvailable        bool
	pickerTab            int // 0 windows, 1 applications
	apps                 []desktop.Application
	appIcons             map[string]ui.ImageSource
	appSearch, appStatus string
	displaysAvailable    bool
	displays             []desktop.Display
	display              desktop.Display
	targetRevision       int
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

	unlock  desktopUnlockState
	privacy mobileDesktopPrivacy
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
	p.unlock.key = desktopUnlockKey(m.currentDesktopID())
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
	p.image, p.queue, p.text, p.unlock.text = nil, nil, "", ""
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
	p.active, p.image, p.queue, p.text, p.unlock.text = false, nil, nil, "", ""
	m.desktopStage(p, "ended", tr("Control session ended"), message, false)
	p.cancel()
	go p.client.stop()
	m.desktopRelease(p)
	invalidate()
}

func (m *mobileApp) desktopInitialize(p *mobileDesktopPage) {
	m.desktopStage(p, "connecting", tr("Connecting to Mac"), tr("If your Mac is asleep, its screen will wake first."), true)
	m.desktopDo(p, func(ctx context.Context) func() {
		s, err := p.client.session(ctx)
		if err != nil {
			return func() { m.desktopFail(p, tr("Connection incomplete"), err) }
		}
		var displays []desktop.Display
		if s.Pick && s.Displays {
			displays, err = p.client.displays(ctx)
			if err != nil {
				return func() { m.desktopFail(p, tr("Unable to load desktops"), err) }
			}
		}
		if s.Pick && s.WindowOnly && s.Window != nil && s.Window.ID == 0 {
			return func() {
				p.pickable, p.windowOnly, p.appsAvailable = true, true, s.Apps
				m.desktopInitPrivacy(p, s)
				p.displaysAvailable, p.displays = s.Displays, displays
				m.desktopChoose(p)
			}
		}
		if !s.WindowOnly || s.Window == nil {
			return func() {
				p.windowOnly, p.pickable, p.appsAvailable = false, s.Pick, s.Apps
				m.desktopInitPrivacy(p, s)
				p.displaysAvailable, p.displays = s.Pick && s.Displays, displays
				if p.displaysAvailable {
					if s.Display != nil {
						for _, display := range displays {
							if display.ID == s.Display.ID && display.UUID == s.Display.UUID {
								m.desktopSelectDisplay(p, display)
								return
							}
						}
					} else if len(displays) == 1 {
						m.desktopSelectDisplay(p, displays[0])
						return
					}
					m.desktopPresentDisplays(p, displays)
					return
				}
				m.desktopStart(p)
			}
		}
		want := *s.Window
		windows, err := p.client.windows(ctx)
		return func() {
			p.windowOnly, p.secret, p.pickable = true, true, s.Pick
			p.appsAvailable = s.Apps
			m.desktopInitPrivacy(p, s)
			p.displaysAvailable, p.displays = s.Pick && s.Displays, displays
			p.keyboard = true // authorization windows usually need a password
			if err != nil {
				m.desktopFail(p, tr("Connection incomplete"), err)
				return
			}
			p.windows = windows
			for _, w := range windows {
				if w.ID == want.ID && w.PID == want.PID {
					m.desktopSelect(p, w)
					return
				}
			}
			m.desktopFinish(p, tr("The permission window closed. Return to the conversation to view the result."))
		}
	})
}

func (m *mobileApp) desktopChoose(p *mobileDesktopPage) {
	// Switching revokes input: the next window needs its own frame first.
	p.invalidateTarget()
	p.pickerTab, p.appStatus = 0, ""
	m.desktopStage(p, "picker", tr("Select a window to control"), "", true)
	m.desktopDo(p, func(ctx context.Context) func() {
		windows, err := p.client.windows(ctx)
		return func() {
			if err != nil {
				m.desktopFail(p, tr("Unable to load windows"), err)
				return
			}
			p.windows, p.busy, p.thumbs = windows, false, nil
			m.desktopLoadThumbs(p)
		}
	})
}

func (m *mobileApp) desktopSelect(p *mobileDesktopPage, w desktop.Window) {
	p.invalidateTarget()
	m.desktopStage(p, "connecting", tr("Opening window"), tr("Checking the connection and system permissions."), true)
	m.desktopDo(p, func(ctx context.Context) func() {
		err := p.client.selectWindow(ctx, w)
		return func() {
			if err != nil {
				m.desktopFail(p, tr("Unable to open window"), err)
				return
			}
			p.window, p.windowOnly, p.zoom, p.panX, p.panY = w, true, 1, 0, 0
			p.display = desktop.Display{}
			m.desktopStart(p)
		}
	})
}

func (m *mobileApp) desktopStart(p *mobileDesktopPage) {
	if p.active {
		return
	}
	title := tr("Connecting to Mac")
	if p.windowOnly {
		title = tr("Opening window")
	}
	m.desktopStage(p, "connecting", title, tr("If your Mac is asleep, its screen will wake first."), true)
	m.desktopDo(p, func(ctx context.Context) func() {
		err := p.client.start(ctx)
		return func() {
			if err != nil {
				m.desktopFail(p, tr("Controls unavailable"), err)
				return
			}
			p.active, p.status = true, tr("Loading screen…")
			m.desktopCheckSavedUnlock(p)
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
		return tr("Connected · ") + textutil.FirstNonEmpty(p.window.Title, p.window.App, tr("Permission window"))
	}
	if p.display.ID != 0 {
		return tr("Connected · ") + p.displayTitle()
	}
	return tr("Connected · Full desktop")
}

// desktopPoll fetches frames one at a time; a slow frame never blocks input.
func (m *mobileApp) desktopPoll(p *mobileDesktopPage) {
	if !p.active || p.polling {
		return
	}
	p.polling = true
	revision := p.targetRevision
	m.desktopDo(p, func(ctx context.Context) func() {
		data, locked, err := p.client.frame(ctx)
		var image ui.ImageSource
		if err == nil {
			if image, _ = ui.DecodeImage(data); image == nil {
				err = fmt.Errorf("%s", tr("Unable to decode the screen. Retrying."))
			}
		}
		return func() {
			if revision == p.targetRevision {
				m.receiveDesktopFrame(p, data, image, locked, err)
			}
		}
	})
}

func (m *mobileApp) receiveDesktopFrame(p *mobileDesktopPage, data []byte, image ui.ImageSource, locked bool, err error) {
	p.polling = false
	if !p.active {
		return
	}
	if locked {
		m.desktopLockChanged(p, true)
	}
	delay := 300 * time.Millisecond
	switch status := desktopStatus(err); {
	case err == nil:
		hash := sha256.Sum256(data)
		if p.image == nil || hash != p.imageHash {
			p.image, p.imageHash = image, hash
		}
		p.status = p.connectedStatus()
		m.desktopLockChanged(p, locked)
	case status == 410:
		m.desktopFinish(p, err.Error())
		return
	case status == 409 && !p.windowOnly && p.displaysAvailable:
		m.desktopChooseDisplays(p)
		p.appStatus = tr("The display changed or disconnected. Select a desktop.")
		return
	case status == 403:
		m.desktopFail(p, tr("Permission required on Mac"), err)
		return
	case status == 423:
		delay = 750 * time.Millisecond
		p.status = tr("Mac is locked")
	case status == 429:
		delay = 350 * time.Millisecond
		p.status = tr("Waiting for Mac…")
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
	revision := p.targetRevision
	m.desktopDo(p, func(ctx context.Context) func() {
		a, err := p.client.actions(ctx)
		return func() {
			if revision != p.targetRevision {
				return
			}
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
	p.actionPending, p.actionStatus = true, tr("Checking permission result…")
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
				p.actionStatus = tr("Permission result unconfirmed. Check the window state and retry.") + err.Error()
			} else {
				p.actionStatus = tr("Check the window instructions. If permission is still pending, retry here.")
			}
			m.desktopRefreshActions(p)
		}
	})
}

// desktopSend keeps input in order. Consecutive moves keep the latest;
// consecutive scrolls add up, so a fast swipe cannot flood the Mac.
func (m *mobileApp) desktopSend(p *mobileDesktopPage, in desktop.Input) {
	if !p.active || p.image == nil || p.unlock.locked || p.unlock.busy {
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
			p.status = tr("Too many actions. Please wait.")
			return
		}
	}
	p.queue = append(p.queue, in)
	p.markTap(in, time.Now())
	m.desktopPump(p)
}

func (m *mobileApp) desktopPump(p *mobileDesktopPage) {
	if p.sending || len(p.queue) == 0 || !p.active || p.unlock.locked || p.unlock.busy {
		return
	}
	in := p.queue[0]
	p.queue = p.queue[1:]
	p.sending = true
	revision, display := p.targetRevision, p.display
	if p.windowOnly {
		display = desktop.Display{}
	}
	m.desktopDo(p, func(ctx context.Context) func() {
		err := p.client.inputForDisplay(ctx, in, display)
		return func() {
			if revision != p.targetRevision {
				return
			}
			p.sending = false
			switch {
			case desktopStatus(err) == 410:
				m.desktopFinish(p, err.Error())
				return
			case err != nil && in.Kind != "move" && in.Kind != "scroll":
				p.status = tr("Delivery unconfirmed. Check the screen before retrying.") + err.Error()
			case err == nil && in.Kind == "text":
				p.status = tr("Sent to the window. Check the screen to confirm.")
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
	vw, vh := p.view.W, p.view.H
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
	if !p.active || p.image == nil || p.unlock.locked || p.unlock.busy {
		return false
	}
	g := &p.pointer
	switch ev.Kind {
	case ui.InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		for _, o := range p.overlays {
			if o.Contains(ev.X, ev.Y) {
				return false
			}
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
		p.status = tr("Send up to 4096 bytes of text at a time")
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
			title := tr("Remote desktop")
			if p.windowOnly {
				title = textutil.FirstNonEmpty(p.window.Title, p.window.App, tr("Permission window"))
			}
			if !p.windowOnly && p.display.ID != 0 {
				title = p.displayTitle()
			}
			if p.phase == "picker" {
				title = tr("Select window")
				if p.pickerTab == 1 {
					title = tr("Apps")
				}
				if p.pickerTab == 2 {
					title = tr("Full desktop")
				}
			}
			ui.Column(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Gap(2).Children(func() {
				ui.Text(c, title).TextAlign(ui.Center).FontSize(m.font(16)).FontWeight(600).MaxLines(1)
				if p.phase == "live" && p.status != "" {
					ui.Text(c, p.status).TextAlign(ui.Center).FontSize(m.font(12)).TextColor(pal.muted).MaxLines(1)
				}
			})
			if p.phase == "picker" && !p.busy {
				if m.circle(c, "desktop-refresh-windows", tr("Refresh list"), func() { icon(c, "refresh", 20, pal.ink) }).Clicked() {
					if p.pickerTab == 1 {
						m.desktopChooseApps(p)
					} else if p.pickerTab == 2 {
						m.desktopChooseDisplays(p)
					} else {
						m.desktopChoose(p)
					}
				}
			} else if (p.windowOnly || p.pickable) && (p.phase == "live" || p.phase == "error") {
				label := tr("Switch window")
				if !p.windowOnly && p.displaysAvailable {
					label = tr("Switch desktop")
				}
				if m.circle(c, "desktop-choose", label, func() { icon(c, "stack-2", 20, pal.ink) }).Clicked() {
					if !p.windowOnly && p.displaysAvailable {
						m.desktopChooseDisplays(p)
					} else {
						m.desktopChoose(p)
					}
				}
			} else {
				// Balance the back button so the title stays centered.
				ui.Box(c).Size(44, 44).Shrink(0)
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
			if m.pill(c, "desktop-retry", tr("Reconnect"), true).Clicked() {
				m.desktopRetry(p)
			}
		}
		if p.phase == "error" || p.phase == "ended" {
			if m.pill(c, "desktop-return", tr("Back to conversation"), p.phase == "ended").Clicked() {
				m.router().Back()
			}
		}
	})
}

func (m *mobileApp) desktopPickerView(c *ui.Context, p *mobileDesktopPage) {
	pal := m.pal
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(16).Gap(10).Children(func() {
			if p.pickable && (p.appsAvailable || p.displaysAvailable) {
				ui.Row(c).FillWidth().Gap(8).Children(func() {
					if p.displaysAvailable && m.pill(c, "desktop-tab-displays", tr("Desktops"), p.pickerTab == 2).Grow(1).Basis(0).MinWidth(0).Disabled(p.busy).Clicked() {
						m.desktopChooseDisplays(p)
					}
					if m.pill(c, "desktop-tab-windows", tr("Windows"), p.pickerTab == 0).Grow(1).Basis(0).MinWidth(0).Disabled(p.busy).Clicked() {
						m.desktopChoose(p)
					}
					if p.appsAvailable && m.pill(c, "desktop-tab-apps", tr("Apps"), p.pickerTab == 1).Grow(1).Basis(0).MinWidth(0).Disabled(p.busy).Clicked() {
						m.desktopChooseApps(p)
					}
				})
			}
			if p.pickerTab == 2 {
				m.desktopDisplayList(c, p)
				return
			}
			if p.pickerTab == 1 {
				m.desktopApplicationList(c, p)
				return
			}
			if p.appStatus != "" {
				ui.Text(c, p.appStatus).FontSize(m.font(14)).TextColor(pal.muted).LineHeight(1.5)
			}
			ui.Text(c, tr("Only the selected window is shared. Complete Touch ID or local confirmation on your Mac when required.")).FontSize(m.font(14)).TextColor(pal.muted).LineHeight(1.5)
			if p.busy {
				ui.Spinner(c).Margin(12)
				return
			}
			if len(p.windows) == 0 {
				ui.Text(c, tr("No shareable windows.")).FontSize(m.font(15)).TextColor(pal.muted)
			}
			m.desktopWindowGrid(c, p)
		})
	})
}

// desktopKeyDef is a key cap: cap is drawn, label is read by VoiceOver.
type desktopKeyDef struct{ key, cap, label string }

var desktopKeys = []desktopKeyDef{{"escape", "esc", "Esc"}, {"tab", "⇥", "Tab"}, {"backspace", "⌫", tr("Backspace")}, {"enter", "⏎", tr("Return")}}
var desktopArrowKeys = []desktopKeyDef{{"left", "←", "←"}, {"up", "↑", "↑"}, {"down", "↓", "↓"}, {"right", "→", "→"}}
var desktopLetterKeys = []desktopKeyDef{{"a", "A", "A"}, {"c", "C", "C"}, {"v", "V", "V"}, {"z", "Z", "Z"}, {"l", "L", "L"}}
var desktopModifiers = []desktopKeyDef{{"command", "⌘", "⌘"}, {"control", "⌃", "⌃"}, {"shift", "⇧", "⇧"}, {"option", "⌥", "⌥"}}

var desktopKeySymbols = map[string]string{"escape": "escape", "tab": "arrow.right.to.line", "backspace": "delete.left", "enter": "return",
	"left": "arrow.left", "up": "arrow.up", "down": "arrow.down", "right": "arrow.right",
	"command": "command", "control": "control", "shift": "shift", "option": "option"}

func (m *mobileApp) desktopLiveView(c *ui.Context, p *mobileDesktopPage) {
	pal := m.pal
	vp := ui.Column(c.Key("desktop-viewport")).FillWidth().Grow(1).MinHeight(0).Clip().Background(pal.bg).Label(tr("Live Mac screen. Tap, swipe, or pinch to zoom."))
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
	p.overlays = p.overlays[:0]
	vp.Children(func() {
		if haveImage {
			ui.Image(c, p.image).Absolute().Left(r.X).Top(r.Y).Width(r.W).Height(r.H).Fit(ui.FillBox)
		} else {
			ui.Column(c).Fill().Gap(10).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				if !p.unlock.locked {
					ui.Spinner(c)
				}
				ui.Text(c, p.status).FontSize(m.font(14)).TextColor(pal.muted).TextAlign(ui.Center).MaxLines(3)
			})
		}
		if !p.unlock.locked {
			m.desktopControls(c, p)
		}
	})
	hasActions := p.windowOnly && !p.actionsUnsupported && len(p.actions.Actions) > 0
	unlock := p.canUnlock() && (p.unlock.locked || p.unlock.status != "")
	typing := !p.unlock.locked && p.keyboard
	if !hasActions && !unlock && !typing && p.actionStatus == "" {
		return // the screen keeps the whole page
	}
	ui.Column(c).FillWidth().Padding(8, 12, 8, 12).Gap(8).Children(func() {
		m.desktopUnlockView(c, p)
		if hasActions {
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
		if typing {
			m.desktopInputBar(c, p)
		}
	})
}

// desktopControls floats a small capsule over the bottom of the screen, as
// remote desktop apps do; the options panel opens above it.
func (m *mobileApp) desktopControls(c *ui.Context, p *mobileDesktopPage) {
	if p.view.W == 0 {
		return
	}
	pal := m.pal
	hasMore := !p.trackpad || (p.canUnlock() && p.unlock.saved) || (p.pickable && p.privacy.available)
	if !hasMore {
		p.more = false
	}
	n := float32(2)
	if hasMore {
		n = 3
	}
	if p.pickable && p.appsAvailable {
		n++
	}
	const size, gap, pad = 44, 4, 4
	w, h := n*size+(n-1)*gap+2*pad, float32(size+2*pad)
	x, y := (p.view.W-w)/2, p.view.H-h-16
	p.overlays = append(p.overlays, ui.Rect{X: x, Y: y, W: w, H: h})
	if p.more {
		pw := min(p.view.W-24, 340)
		m.desktopMorePanel(c, p, (p.view.W-pw)/2, p.view.H-y+10, pw)
	}
	ui.Row(c.Key("desktop-controls")).Absolute().Left(x).Top(y).Width(w).Height(h).Padding(pad).Gap(gap).AlignItems(ui.Center).
		Radius(h/2).Background(pal.surface).Border(1, pal.line).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.22)).Children(func() {
		if p.pickable && p.appsAvailable && m.desktopControl(c, "desktop-apps", "apps", tr("Open app"), false).Clicked() {
			c.Blur()
			m.desktopChooseApps(p)
		}
		if m.desktopControl(c, "desktop-keyboard", "keyboard", tr("Keyboard"), p.keyboard).Clicked() {
			p.keyboard, p.more, p.moreKeys = !p.keyboard, false, false
			p.focusText = p.keyboard
			if !p.keyboard {
				c.Blur()
			}
		}
		label, glyph := tr("Trackpad"), "pointer"
		if !p.trackpad {
			label, glyph = tr("Touch"), "hand-stop"
		}
		if m.desktopControl(c, "desktop-pointer", glyph, tr("Switch control mode, currently ")+label, false).Clicked() {
			p.trackpad, p.pointer = !p.trackpad, desktopPointer{}
			prefs := map[bool]string{true: "trackpad", false: "touch"}[p.trackpad]
			path := m.desktopPrefsFile()
			go func() { _ = writeDesktopPrefs(path, prefs) }()
		}
		if hasMore && m.desktopControl(c, "desktop-more-options", "dots", tr("More options"), p.more).Clicked() {
			p.more = !p.more
		}
	})
}

// desktopControl is a round icon button in the floating capsule.
func (m *mobileApp) desktopControl(c *ui.Context, key, glyph, label string, on bool) ui.Element {
	fg := m.pal.ink
	b := m.phoneButton(c.Key(key)).Size(44, 44).Padding(0).Radius(22).Justify(ui.Center).Label(label)
	if on {
		b.Background(m.pal.ink)
		fg = m.pal.bg
	}
	b.Children(func() { icon(c, glyph, 22, fg) })
	return b
}

// desktopMorePanel holds the occasional settings: how touches act in touch
// mode and the saved login password.
func (m *mobileApp) desktopMorePanel(c *ui.Context, p *mobileDesktopPage, left, bottom, width float32) {
	pal := m.pal
	panel := ui.Column(c.Key("desktop-more-panel")).Absolute().Left(left).Bottom(bottom).Width(width).Padding(12).Gap(12).
		Radius(18).Background(pal.surface).Border(1, pal.line).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.22))
	if b := panel.Bounds(); b.H > 0 {
		p.overlays = append(p.overlays, ui.Rect{X: left, Y: p.view.H - bottom - b.H, W: width, H: b.H})
	}
	panel.Children(func() {
		ui.Scroll(c).FillWidth().MaxHeight(max(44, p.view.H-bottom-36)).Children(func() {
			ui.Column(c).FillWidth().Gap(12).Children(func() {
				m.desktopPrivacyView(c, p)
				if !p.trackpad {
					ui.Segmented(c.Key("desktop-touch-mode"), &p.touchMode, tr("Click / Scroll"), tr("Drag"), tr("Move screen")).FillWidth()
				}
				if p.canUnlock() && p.unlock.saved {
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						icon(c, "shield-check", 18, pal.ok)
						ui.Text(c, tr("Password saved for this Mac")).Grow(1).MinWidth(0).FontSize(m.font(14)).MaxLines(1)
						forget := m.phoneButton(c.Key("desktop-forget-saved")).PaddingX(8).Justify(ui.Center).Label(tr("Forget password")).Disabled(p.unlock.busy)
						forget.Children(func() {
							ui.Text(c, tr("Forget password")).FontSize(m.font(14)).FontWeight(500).TextColor(pal.warn)
						})
						if forget.Clicked() {
							p.more = false
							m.desktopForgetUnlock(p)
						}
					})
				}
			})
		})
	})
}

// desktopInputBar is a message-style capsule: the lock toggles password
// entry and the round arrow sends. Keys ride above the system keyboard.
func (m *mobileApp) desktopInputBar(c *ui.Context, p *mobileDesktopPage) {
	pal := m.pal
	svc := c.Services()
	handler := func(id string) { m.desktopAccessory(p, svc, id) }
	ui.Row(c.Key("desktop-input")).FillWidth().MinHeight(46).Padding(0, 2).Gap(2).AlignItems(ui.Center).Background(pal.surface).Radius(23).Children(func() {
		// A secure field forces iOS's ASCII keyboard without input methods,
		// so hide the text only when the person chooses password entry.
		mode, other, glyph := tr("Text"), tr("Password"), "lock-open"
		if p.secret {
			mode, other, glyph = other, mode, "lock"
		}
		secret := m.phoneButton(c.Key("desktop-secret")).Size(44, 44).Padding(0).Radius(22).Justify(ui.Center).KeepFocus().
			Label(tr("Current: ") + mode + tr(" input, switch to ") + other + tr(" input"))
		secret.Children(func() {
			color := pal.muted
			if p.secret {
				color = pal.ink
			}
			icon(c, glyph, 20, color)
		})
		if secret.Clicked() {
			p.secret = !p.secret
		}
		field := ui.TextInput(c.Key("desktop-text"), &p.text).Grow(1).MinWidth(0).MinHeight(44).Padding(10, 4).Background(pal.surface).Border(1, pal.surface).
			InputOptions(ui.InputOptions{Correction: ui.CorrectionOff, Capitalization: ui.CapitalizeNone, Return: ui.ReturnGo})
		if p.secret {
			field.Password().Label(tr("Password to send to Mac")).Placeholder(tr("Enter password"))
		} else {
			field.Label(tr("Text to send to Mac")).Placeholder(tr("Enter text to send"))
		}
		field.InputAccessory(p.accessoryActions(), handler)
		if p.focusText {
			field.Focus()
			p.focusText = false
		}
		if field.Submitted() {
			m.desktopSendText(p)
		}
		ready := p.text != ""
		send := m.phoneButton(c.Key("desktop-send")).Size(44, 44).Padding(0).Radius(22).Justify(ui.Center).KeepFocus().Label(tr("Send")).Disabled(!ready)
		send.Children(func() {
			bg := pal.line
			if ready {
				bg = pal.ink
			}
			ui.Row(c).Size(34, 34).Radius(17).Background(bg).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				icon(c, "arrow-up", 18, pal.bg)
			})
		})
		if send.Clicked() {
			m.desktopSendText(p)
		}
	})
	// iOS draws the keys natively above its keyboard; elsewhere, as in tests,
	// the same actions show as a Go bar.
	if runtime.GOOS != "ios" {
		ui.InputAccessoryBar(c, p.accessoryActions(), &p.moreKeys, handler)
	}
}

// accessoryActions are the Mac keys above the software keyboard: the common
// four, a panel of modifiers, arrows and shortcut letters, and hide.
func (p *mobileDesktopPage) accessoryActions() []ui.InputAction {
	var actions []ui.InputAction
	for _, k := range desktopKeys {
		actions = append(actions, ui.InputAction{ID: "key-" + k.key, Label: k.label, Symbol: desktopKeySymbols[k.key]})
	}
	more := ui.InputAction{ID: "more", Label: tr("More"), Symbol: "ellipsis"}
	for _, k := range desktopModifiers {
		more.Items = append(more.Items, ui.InputAction{ID: "mod-" + k.key, Label: k.label, Symbol: desktopKeySymbols[k.key], Selected: p.modifier == k.key})
	}
	for _, keys := range [][]desktopKeyDef{desktopArrowKeys, desktopLetterKeys} {
		for _, k := range keys {
			more.Items = append(more.Items, ui.InputAction{ID: "key-" + k.key, Label: k.label, Symbol: desktopKeySymbols[k.key]})
		}
	}
	return append(actions, more, ui.InputAction{ID: "hide", Label: tr("Hide keyboard"), Symbol: "keyboard.chevron.compact.down"})
}

func (m *mobileApp) desktopAccessory(p *mobileDesktopPage, svc ui.Services, id string) {
	if m.desktopPage != p {
		return
	}
	switch {
	case id == "hide":
		p.keyboard, p.moreKeys = false, false
		svc.Blur()
	case strings.HasPrefix(id, "mod-"):
		if k := strings.TrimPrefix(id, "mod-"); p.modifier == k {
			p.modifier = ""
		} else {
			p.modifier = k
		}
	case strings.HasPrefix(id, "key-"):
		m.desktopKey(p, strings.TrimPrefix(id, "key-"))
	}
	invalidate()
}

// desktopSelectFull switches a menu lease to the full desktop.
func (m *mobileApp) desktopSelectFull(p *mobileDesktopPage) {
	if !p.pickable {
		return
	}
	if p.displaysAvailable {
		m.desktopChooseDisplays(p)
		return
	}
	p.invalidateTarget()
	m.desktopStage(p, "connecting", tr("Connecting to Mac"), tr("Switching to full desktop."), true)
	m.desktopDo(p, func(ctx context.Context) func() {
		err := p.client.selectWindow(ctx, desktop.Window{})
		return func() {
			if err != nil {
				m.desktopFail(p, tr("Unable to switch"), err)
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
	if !p.pickable || p.thumbBusy || p.phase != "picker" || p.pickerTab != 0 || p.busy {
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

// desktopWindowGrid shows the full desktop, when offered, and the shareable
// windows as preview cards two to a row.
func (m *mobileApp) desktopWindowGrid(c *ui.Context, p *mobileDesktopPage) {
	var cards []func()
	if p.pickable && !p.displaysAvailable {
		cards = append(cards, func() { m.desktopFullCard(c, p) })
	}
	for _, w := range p.windows {
		cards = append(cards, func() { m.desktopWindowCard(c, p, w) })
	}
	for i := 0; i < len(cards); i += 2 {
		row := cards[i:min(i+2, len(cards))]
		ui.Row(c.Key(fmt.Sprintf("desktop-window-row-%d", i))).FillWidth().Gap(10).AlignItems(ui.Start).Children(func() {
			for _, card := range row {
				card()
			}
			if len(row) == 1 {
				ui.Column(c).Grow(1).Basis(0).MinWidth(0)
			}
		})
	}
}

func (m *mobileApp) desktopFullCard(c *ui.Context, p *mobileDesktopPage) {
	pal := m.pal
	card := m.phoneButton(c.Key("desktop-full")).Column().Grow(1).Basis(0).MinWidth(0).Gap(6).Padding(8).Background(pal.surface).Radius(12).Label(tr("Full desktop"))
	card.Children(func() {
		ui.Column(c).FillWidth().AspectRatio(1.6).Radius(8).Background(pal.bg).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			icon(c, "device-desktop", 32, pal.muted)
		})
		ui.Text(c, tr("Full desktop")).FontSize(m.font(14)).FontWeight(600).LineHeight(1.3).MaxLines(2)
	})
	if card.Clicked() {
		m.desktopSelectFull(p)
	}
}

func (m *mobileApp) desktopWindowCard(c *ui.Context, p *mobileDesktopPage, w desktop.Window) {
	pal := m.pal
	key := desktopWindowKey(w)
	img, loaded := p.thumbs[key]
	title := textutil.FirstNonEmpty(w.Title, w.App, tr("Untitled window"))
	card := m.phoneButton(c.Key("desktop-window-" + key)).Column().Grow(1).Basis(0).MinWidth(0).Gap(6).Padding(8).Background(pal.surface).Radius(12)
	card.Children(func() {
		ui.Column(c).FillWidth().AspectRatio(1.6).Radius(8).Clip().Background(pal.bg).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			switch {
			case img != nil:
				ui.Image(c, img).Fill().Fit(ui.Contain)
			case !loaded && p.pickable:
				ui.Spinner(c)
			default:
				ui.Text(c, textutil.FirstNonEmpty(w.App, tr("Window"))).FontSize(m.font(12)).TextColor(pal.muted).MaxLines(1)
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
