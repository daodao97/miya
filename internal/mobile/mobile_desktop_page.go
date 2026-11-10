package mobile

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/keyboardbar"
	"github.com/egoist/mygo/ui"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/remote"
	"miya/internal/textutil"
	"miya/internal/uiruntime"
	"miya/internal/widgets"
)

type DesktopPoint struct{ X, Y float64 }

type desktopPointer struct {
	active                       bool
	startX, startY, lastX, lastY float32
	at                           time.Time
	moved, drag                  bool
	from                         DesktopPoint
	seq                          int
}

type MobileDesktopPage struct {
	Client   *control.Client
	Conn     *remote.Conn
	released bool
	Ctx      context.Context
	Cancel   context.CancelFunc
	gen      int

	Phase                          string // connecting, picker, live, error, paused, ended
	suspended, resuming, restoring bool
	resumeBlocked                  bool
	resumePicker                   bool
	owner                          remote.Request
	desktopLink                    string
	Title, Detail                  string
	Busy                           bool

	WindowOnly bool
	Window     desktop.Window
	Windows    []desktop.Window

	Active, Polling bool
	Image           ui.ImageSource
	ImageHash       [32]byte
	Status          string

	Zoom, PanX, panY float32
	View             ui.Rect
	Trackpad         bool
	TouchMode        int // 0 tap and swipe, 1 drag, 2 move the zoomed view
	Cursor           DesktopPoint
	pointer          desktopPointer
	lastTap          time.Time
	scrollX, scrollY float32

	text     string
	modifier string
	moreKeys bool
	// keyboard shows the text field and keys; otherwise the screen gets the room.
	keyboard bool
	// focusText raises the system keyboard once; more opens the options panel.
	focusText, More bool
	// overlays are the floating controls over the screen, in viewport DIPs;
	// touches there never reach the Mac.
	overlays []ui.Rect
	// secret hides typed text; authorization windows start with it on.
	Secret bool
	// pickable leases came from the menu and may choose any window.
	Pickable             bool
	AppsAvailable        bool
	PickerTab            int // 0 windows, 1 applications
	Apps                 []desktop.Application
	appIcons             map[string]ui.ImageSource
	AppSearch, appStatus string
	DisplaysAvailable    bool
	Displays             []desktop.Display
	Display              desktop.Display
	targetRevision       int
	// thumbs holds picker previews by window; nil marks one that failed.
	Thumbs    map[string]ui.ImageSource
	ThumbBusy bool

	// taps are recent clicks shown as a fading ripple where they landed.
	Taps    []desktopTap
	Queue   []desktop.Input
	Sending bool

	actions                                  control.Actions
	actionsFresh, actionsBusy, actionPending bool
	actionsUnsupported                       bool
	actionStatus                             string

	Unlock  DesktopUnlockState
	Privacy mobileDesktopPrivacy

	// Floating keeps the session in an in-app floating window while other
	// pages show; leaving the page then does not end control.
	Floating bool
	float    desktopFloat
}

func (m *App) desktopPrefsFile() string {
	if m.DataDir == "" {
		return ""
	}
	return filepath.Join(m.DataDir, "desktop-control.json")
}

func (m *App) desktopUIOpen() bool { return m.FilesCancel != nil || m.DesktopPage != nil }

func (m *App) newDesktopPage(conn *remote.Conn, token string) *MobileDesktopPage {
	ctx, cancel := context.WithCancel(context.Background())
	client := control.NewClient(conn.DialFiles, token)
	if m.desktopClient != nil {
		client = m.desktopClient(conn, token)
	}
	p := &MobileDesktopPage{Client: client, Conn: conn, Ctx: ctx, Cancel: cancel, gen: m.Generation,
		Zoom: 1, Cursor: DesktopPoint{.5, .5}, Trackpad: control.ReadPrefs(m.desktopPrefsFile()).PointerMode != "touch"}
	p.Unlock.Key = DesktopUnlockKey(m.CurrentDesktopID())
	p.owner, p.desktopLink = m.desktopOpening, m.Link
	return p
}

func (m *App) openDesktopPage(conn *remote.Conn, token string) {
	p := m.newDesktopPage(conn, token)
	m.DesktopPage = p
	m.Router().Push("/desktop")
	m.DesktopInitialize(p)
}

// closeDesktopPage revokes the lease and forgets the page. back also leaves
// the page, as when the approval it served has been resolved.
func (m *App) CloseDesktopPage(back bool) {
	p := m.DesktopPage
	if p == nil {
		return
	}
	m.DesktopPage = nil
	if p.resuming {
		m.stopDesktopOpen()
	}
	m.DesktopApproval = MobileDesktopApproval{}
	m.desktopRelease(p)
	if p.Phase != "ended" {
		go p.Client.Stop()
	}
	p.Cancel()
	p.Image, p.Queue, p.text, p.Unlock.Text = nil, nil, "", ""
	if back && m.Router().Path() == "/desktop" {
		m.Router().Back()
	}
}

// desktopDo runs work off the main thread and applies its result only while
// the same page is still open and has not ended.
func (m *App) desktopDo(p *MobileDesktopPage, work func(ctx context.Context) func()) {
	ctx := p.Ctx
	go func() {
		apply := work(ctx)
		// A closed page queues nothing more on the main thread.
		if ctx.Err() != nil {
			return
		}
		m.onMain(p.gen, func() {
			if ctx.Err() != nil || m.DesktopPage != p || p.Phase == "ended" {
				return
			}
			apply()
			uiruntime.Invalidate()
		})
	}()
}

func (m *App) desktopAfter(p *MobileDesktopPage, d time.Duration, f func()) {
	ctx := p.Ctx
	time.AfterFunc(d, func() {
		if ctx.Err() != nil {
			return
		}
		m.onMain(p.gen, func() {
			if m.DesktopPage == p && p.Phase != "ended" {
				f()
				uiruntime.Invalidate()
			}
		})
	})
}

func (m *App) DesktopStage(p *MobileDesktopPage, phase, title, detail string, busy bool) {
	p.Phase, p.Title, p.Detail, p.Busy = phase, title, detail, busy
}

func (m *App) desktopFail(p *MobileDesktopPage, title string, err error) {
	if control.Status(err) == 410 {
		m.DesktopFinish(p, err.Error())
		return
	}
	p.Active = false
	m.DesktopStage(p, "error", title, err.Error(), false)
}

// desktopFinish keeps the page with its reason; the lease is already gone
// or is revoked here, and the last frame and any typed text are dropped.
func (m *App) DesktopFinish(p *MobileDesktopPage, message string) {
	if p.Phase == "ended" {
		return
	}
	p.suspended, p.resuming, p.restoring = false, false, false
	p.Active, p.Image, p.Queue, p.text, p.Unlock.Text = false, nil, nil, "", ""
	m.DesktopStage(p, "ended", tr("Control session ended"), message, false)
	p.Cancel()
	go p.Client.Stop()
	m.desktopRelease(p)
	uiruntime.Invalidate()
}

func (m *App) DesktopInitialize(p *MobileDesktopPage) {
	m.DesktopStage(p, "connecting", tr("Connecting to Mac"), tr("If your Mac is asleep, its screen will wake first."), true)
	m.desktopDo(p, func(ctx context.Context) func() {
		s, err := p.Client.Session(ctx)
		if err != nil {
			return func() { m.desktopFail(p, tr("Connection incomplete"), err) }
		}
		var displays []desktop.Display
		if s.Pick && s.Displays {
			displays, err = p.Client.Displays(ctx)
			if err != nil {
				return func() { m.desktopFail(p, tr("Unable to load desktops"), err) }
			}
		}
		if p.restoring {
			var windows []desktop.Window
			if p.WindowOnly && p.Window.ID != 0 {
				windows, err = p.Client.Windows(ctx)
			}
			return func() { m.initializeResumedDesktop(p, s, displays, windows, err) }
		}
		if s.Pick && s.WindowOnly && s.Window != nil && s.Window.ID == 0 {
			return func() {
				p.Pickable, p.WindowOnly, p.AppsAvailable = true, true, s.Apps
				m.DesktopInitPrivacy(p, s)
				p.DisplaysAvailable, p.Displays = s.Displays, displays
				m.desktopChoose(p)
			}
		}
		if !s.WindowOnly || s.Window == nil {
			return func() {
				p.WindowOnly, p.Pickable, p.AppsAvailable = false, s.Pick, s.Apps
				m.DesktopInitPrivacy(p, s)
				p.DisplaysAvailable, p.Displays = s.Pick && s.Displays, displays
				if p.DisplaysAvailable {
					if s.Display != nil {
						for _, display := range displays {
							if display.ID == s.Display.ID && display.UUID == s.Display.UUID {
								m.DesktopSelectDisplay(p, display)
								return
							}
						}
					} else if len(displays) == 1 {
						m.DesktopSelectDisplay(p, displays[0])
						return
					}
					m.DesktopPresentDisplays(p, displays)
					return
				}
				m.desktopStart(p)
			}
		}
		want := *s.Window
		windows, err := p.Client.Windows(ctx)
		return func() {
			p.WindowOnly, p.Secret, p.Pickable = true, true, s.Pick
			p.AppsAvailable = s.Apps
			m.DesktopInitPrivacy(p, s)
			p.DisplaysAvailable, p.Displays = s.Pick && s.Displays, displays
			p.keyboard = true // authorization windows usually need a password
			if err != nil {
				m.desktopFail(p, tr("Connection incomplete"), err)
				return
			}
			p.Windows = windows
			for _, w := range windows {
				if w.ID == want.ID && w.PID == want.PID {
					m.desktopSelect(p, w)
					return
				}
			}
			m.DesktopFinish(p, tr("The permission window closed. Return to the conversation to view the result."))
		}
	})
}

func (m *App) desktopChoose(p *MobileDesktopPage) {
	// Switching revokes input: the next window needs its own frame first.
	p.InvalidateTarget()
	p.PickerTab, p.appStatus = 0, ""
	m.DesktopStage(p, "picker", tr("Select a window to control"), "", true)
	m.desktopDo(p, func(ctx context.Context) func() {
		windows, err := p.Client.Windows(ctx)
		return func() {
			if err != nil {
				m.desktopFail(p, tr("Unable to load windows"), err)
				return
			}
			p.Windows, p.Busy, p.Thumbs = windows, false, nil
			m.desktopLoadThumbs(p)
		}
	})
}

func (m *App) desktopSelect(p *MobileDesktopPage, w desktop.Window) {
	p.InvalidateTarget()
	m.DesktopStage(p, "connecting", tr("Opening window"), tr("Checking the connection and system permissions."), true)
	m.desktopDo(p, func(ctx context.Context) func() {
		err := p.Client.SelectWindow(ctx, w)
		return func() {
			if err != nil {
				m.desktopFail(p, tr("Unable to open window"), err)
				return
			}
			p.Window, p.WindowOnly, p.Zoom, p.PanX, p.panY = w, true, 1, 0, 0
			p.Display = desktop.Display{}
			m.desktopStart(p)
		}
	})
}

func (m *App) desktopStart(p *MobileDesktopPage) {
	if p.Active {
		return
	}
	title := tr("Connecting to Mac")
	if p.WindowOnly {
		title = tr("Opening window")
	}
	m.DesktopStage(p, "connecting", title, tr("If your Mac is asleep, its screen will wake first."), true)
	m.desktopDo(p, func(ctx context.Context) func() {
		err := p.Client.Start(ctx)
		return func() {
			if err != nil {
				m.desktopFail(p, tr("Controls unavailable"), err)
				return
			}
			p.Active, p.Status = true, tr("Loading screen…")
			m.DesktopCheckSavedUnlock(p)
			m.DesktopStage(p, "live", "", "", false)
			m.DesktopPoll(p)
			m.desktopRefreshActions(p)
		}
	})
}

func (m *App) desktopRetry(p *MobileDesktopPage) {
	if p.suspended {
		p.resumeBlocked = false
		m.resumeDesktopPage()
		return
	}
	switch {
	case p.Active:
		m.DesktopPoll(p)
	case p.WindowOnly && p.Window.ID != 0:
		m.desktopStart(p)
	default:
		m.DesktopInitialize(p)
	}
}

func (p *MobileDesktopPage) ConnectedStatus() string {
	if p.WindowOnly {
		return tr("Connected · ") + textutil.FirstNonEmpty(p.Window.Title, p.Window.App, tr("Permission window"))
	}
	if p.Display.ID != 0 {
		return tr("Connected · ") + p.displayTitle()
	}
	return tr("Connected · Full desktop")
}

// desktopPoll fetches frames one at a time; a slow frame never blocks input.
func (m *App) DesktopPoll(p *MobileDesktopPage) {
	if !p.Active || p.Polling {
		return
	}
	p.Polling = true
	revision := p.targetRevision
	m.desktopDo(p, func(ctx context.Context) func() {
		data, locked, err := p.Client.Frame(ctx)
		var image ui.ImageSource
		if err == nil {
			if image, _ = ui.DecodeImage(data); image == nil {
				err = fmt.Errorf("%s", tr("Unable to decode the screen. Retrying."))
			}
		}
		return func() {
			if revision == p.targetRevision {
				m.ReceiveDesktopFrame(p, data, image, locked, err)
			}
		}
	})
}

func (m *App) ReceiveDesktopFrame(p *MobileDesktopPage, data []byte, image ui.ImageSource, locked bool, err error) {
	p.Polling = false
	if !p.Active {
		return
	}
	if locked {
		m.DesktopLockChanged(p, true)
	}
	delay := 300 * time.Millisecond
	switch status := control.Status(err); {
	case err == nil:
		hash := sha256.Sum256(data)
		if p.Image == nil || hash != p.ImageHash {
			p.Image, p.ImageHash = image, hash
		}
		p.Status = p.ConnectedStatus()
		m.DesktopLockChanged(p, locked)
	case status == 410:
		m.DesktopFinish(p, err.Error())
		return
	case status == 409 && !p.WindowOnly && p.DisplaysAvailable:
		m.desktopChooseDisplays(p)
		p.appStatus = tr("The display changed or disconnected. Select a desktop.")
		return
	case status == 403:
		m.desktopFail(p, tr("Permission required on Mac"), err)
		return
	case status == 423:
		delay = 750 * time.Millisecond
		p.Status = tr("Mac is locked")
	case status == 429:
		delay = 350 * time.Millisecond
		p.Status = tr("Waiting for Mac…")
	default:
		delay = 1500 * time.Millisecond
		p.Status = err.Error()
	}
	if p.Floating {
		delay = max(delay, DesktopFloatPollInterval)
	}
	m.desktopAfter(p, delay, func() { m.DesktopPoll(p) })
}

func (m *App) desktopRefreshActions(p *MobileDesktopPage) {
	if !p.WindowOnly || !p.Active || p.actionsBusy || p.actionsUnsupported {
		return
	}
	p.actionsBusy = true
	revision := p.targetRevision
	m.desktopDo(p, func(ctx context.Context) func() {
		a, err := p.Client.Actions(ctx)
		return func() {
			if revision != p.targetRevision {
				return
			}
			p.actionsBusy = false
			switch status := control.Status(err); {
			case err == nil:
				p.actions, p.actionsFresh = a, true
			case status == 410:
				m.DesktopFinish(p, err.Error())
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

func (m *App) desktopPressAction(p *MobileDesktopPage, a desktop.Action) {
	if p.actionPending || !p.actionsFresh || !a.Enabled || !p.Active {
		return
	}
	p.actionPending, p.actionStatus = true, tr("Checking permission result…")
	w := p.actions.Window
	m.desktopDo(p, func(ctx context.Context) func() {
		err := p.Client.PressAction(ctx, a.ID, w)
		return func() {
			p.actionPending, p.actionsFresh = false, false
			if control.Status(err) == 410 {
				m.DesktopFinish(p, err.Error())
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
func (m *App) DesktopSend(p *MobileDesktopPage, in desktop.Input) {
	if !p.Active || p.Image == nil || p.Unlock.Locked || p.Unlock.Busy {
		return
	}
	if n := len(p.Queue); n > 0 {
		last := &p.Queue[n-1]
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
			p.Status = tr("Too many actions. Please wait.")
			return
		}
	}
	p.Queue = append(p.Queue, in)
	p.markTap(in, time.Now())
	m.desktopPump(p)
}

func (m *App) desktopPump(p *MobileDesktopPage) {
	if p.Sending || len(p.Queue) == 0 || !p.Active || p.Unlock.Locked || p.Unlock.Busy {
		return
	}
	in := p.Queue[0]
	p.Queue = p.Queue[1:]
	p.Sending = true
	revision, display := p.targetRevision, p.Display
	if p.WindowOnly {
		display = desktop.Display{}
	}
	m.desktopDo(p, func(ctx context.Context) func() {
		err := p.Client.InputForDisplay(ctx, in, display)
		return func() {
			if revision != p.targetRevision {
				return
			}
			p.Sending = false
			switch {
			case control.Status(err) == 410:
				m.DesktopFinish(p, err.Error())
				return
			case err != nil && in.Kind != "move" && in.Kind != "scroll":
				p.Status = tr("Delivery unconfirmed. Check the screen before retrying.") + err.Error()
			case err == nil && in.Kind == "text":
				p.Status = tr("Sent to the window. Check the screen to confirm.")
			}
			if in.Kind != "move" && in.Kind != "scroll" {
				p.actionsFresh = false
				m.desktopRefreshActions(p)
			}
			m.desktopAfter(p, 30*time.Millisecond, func() { m.desktopPump(p) })
		}
	})
}

func desktopClampInt(v int) int { return max(-1000, min(1000, v)) }

func desktopClamp(v, lo, hi float32) float32 { return max(lo, min(hi, v)) }

func desktopDist(dx, dy float32) float32 {
	return float32(math.Hypot(float64(dx), float64(dy)))
}

// imageRect is where the frame shows inside the viewport, in local DIPs.
func (p *MobileDesktopPage) ImageRect() (ui.Rect, bool) {
	if p.Image == nil {
		return ui.Rect{}, false
	}
	iw, ih := ui.ImageSize(p.Image)
	vw, vh := p.View.W, p.View.H
	if iw <= 0 || ih <= 0 || vw <= 0 || vh <= 0 {
		return ui.Rect{}, false
	}
	s := min(vw/iw, vh/ih) * p.Zoom
	w, h := iw*s, ih*s
	maxX, maxY := max(0, (w-p.View.W)/2), max(0, (h-p.View.H)/2)
	p.PanX, p.panY = desktopClamp(p.PanX, -maxX, maxX), desktopClamp(p.panY, -maxY, maxY)
	return ui.Rect{X: (p.View.W-w)/2 + p.PanX, Y: (p.View.H-h)/2 + p.panY, W: w, H: h}, true
}

func (p *MobileDesktopPage) point(x, y float32) (DesktopPoint, bool) {
	r, ok := p.ImageRect()
	if !ok || !r.Contains(x, y) {
		return DesktopPoint{}, false
	}
	return DesktopPoint{X: float64(desktopClamp((x-r.X)/r.W, 0, 1)), Y: float64(desktopClamp((y-r.Y)/r.H, 0, 1))}, true
}

func (p *MobileDesktopPage) SetZoom(z float32) {
	p.Zoom = desktopClamp(z, 1, 4)
}

// moveCursor accelerates faster swipes, as the previous page did.
func (p *MobileDesktopPage) moveCursor(dx, dy float32) {
	r, ok := p.ImageRect()
	if !ok {
		return
	}
	gain := 1.1 + min(1.4, desktopDist(dx, dy)/24)
	p.Cursor.X = float64(desktopClamp(float32(p.Cursor.X)+dx*gain/r.W, 0, 1))
	p.Cursor.Y = float64(desktopClamp(float32(p.Cursor.Y)+dy*gain/r.H, 0, 1))
	// Keep the cursor on screen when zoomed in.
	cx, cy := r.X+float32(p.Cursor.X)*r.W, r.Y+float32(p.Cursor.Y)*r.H
	const margin = 24
	if cx < margin {
		p.PanX += margin - cx
	} else if cx > p.View.W-margin {
		p.PanX -= cx - (p.View.W - margin)
	}
	if cy < margin {
		p.panY += margin - cy
	} else if cy > p.View.H-margin {
		p.panY -= cy - (p.View.H - margin)
	}
}

func (m *App) DesktopAtCursor(p *MobileDesktopPage, kind string) {
	m.DesktopSend(p, desktop.Input{Kind: kind, X: p.Cursor.X, Y: p.Cursor.Y})
}

func (m *App) DesktopPointerInput(p *MobileDesktopPage, ev ui.InputEvent) bool {
	if !p.Active || p.Image == nil || p.Unlock.Locked || p.Unlock.Busy {
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
		*g = desktopPointer{active: true, startX: ev.X, startY: ev.Y, lastX: ev.X, lastY: ev.Y, at: time.Now(), from: p.Cursor, seq: g.seq + 1}
		if p.Trackpad {
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
		if p.Trackpad {
			p.moveCursor(dx, dy)
			m.DesktopAtCursor(p, "move")
		} else if p.TouchMode == 2 {
			p.PanX, p.panY = p.PanX+dx, p.panY+dy
		} else if p.TouchMode == 0 {
			if at, ok := p.point(g.startX, g.startY); ok {
				m.desktopScroll(p, at, dx*2, dy*2)
			}
		}
		return true
	case ui.InputPointerUp:
		if !g.active {
			return false
		}
		down := *g
		g.active = false
		if ev.Canceled {
			// The system or a touch gesture took the press: nothing it did
			// is sent to the desktop.
			return true
		}
		if p.Trackpad {
			if down.drag {
				if down.moved {
					m.DesktopSend(p, desktop.Input{Kind: "drag", X: down.from.X, Y: down.from.Y, ToX: p.Cursor.X, ToY: p.Cursor.Y})
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
				m.DesktopAtCursor(p, kind)
			}
			return true
		}
		start, startOK := p.point(down.startX, down.startY)
		end, endOK := p.point(ev.X, ev.Y)
		dx, dy := ev.X-down.startX, ev.Y-down.startY
		switch {
		case p.TouchMode == 2:
		case p.TouchMode == 1:
			if startOK && endOK {
				m.DesktopSend(p, desktop.Input{Kind: "drag", X: start.X, Y: start.Y, ToX: end.X, ToY: end.Y})
			}
		case desktopDist(dx, dy) > 10:
			// Moves stream scrolls while the finger is down. Only a release
			// without move events needs the full displacement as a fallback.
			if startOK && !down.moved {
				m.desktopScroll(p, start, dx*2, dy*2)
			}
		case endOK && !down.moved:
			m.DesktopSend(p, desktop.Input{Kind: "click", X: end.X, Y: end.Y})
		}
		return true
	case ui.InputScroll:
		// UIKit's keyboard container and touch-scroll arbitration deliver
		// scrolls directly, without a pointer-up. Their deltas are offsets;
		// CGEvent takes the inverse, matching a finger dragging the content.
		switch {
		case p.Trackpad:
			m.desktopScroll(p, p.Cursor, -ev.DX*2, -ev.DY*2)
		case p.TouchMode == 0:
			if at, ok := p.point(ev.X, ev.Y); ok {
				m.desktopScroll(p, at, -ev.DX*2, -ev.DY*2)
			}
		case p.TouchMode == 2:
			p.PanX, p.panY = p.PanX-ev.DX, p.panY-ev.DY
		}
		return true
	case ui.InputZoom:
		if ev.Scale > 0 {
			p.SetZoom(p.Zoom * ev.Scale)
		}
		return true
	}
	return false
}

func (m *App) desktopScroll(p *MobileDesktopPage, at DesktopPoint, dx, dy float32) {
	p.scrollX, p.scrollY = p.scrollX+dx, p.scrollY+dy
	x, y := int(p.scrollX), int(p.scrollY)
	if x == 0 && y == 0 {
		return
	}
	p.scrollX, p.scrollY = p.scrollX-float32(x), p.scrollY-float32(y)
	m.DesktopSend(p, desktop.Input{Kind: "scroll", X: at.X, Y: at.Y, DeltaX: desktopClampInt(x), DeltaY: desktopClampInt(y)})
}

func (m *App) desktopGestures(p *MobileDesktopPage, events []ui.GestureEvent) {
	for _, e := range events {
		switch e.Kind {
		case "pinch":
			if e.Scale > 0 {
				p.SetZoom(p.Zoom * e.Scale)
			}
		case "pan":
			if p.Trackpad && p.Active {
				m.desktopScroll(p, p.Cursor, e.DX*2, e.DY*2)
			} else {
				p.PanX, p.panY = p.PanX+e.DX, p.panY+e.DY
			}
		case "tap":
			if e.Phase == "end" && p.Trackpad {
				m.DesktopAtCursor(p, "rightClick")
			}
		}
	}
}

func (m *App) desktopKey(p *MobileDesktopPage, key string) {
	m.DesktopSend(p, desktop.Input{Kind: "key", Key: key, Modifier: p.modifier})
	p.modifier = ""
}

func (m *App) desktopSendText(p *MobileDesktopPage) {
	text := p.text
	if text == "" || !p.Active {
		return
	}
	if len(text) > 4096 {
		p.Status = tr("Send up to 4096 bytes of text at a time")
		return
	}
	p.text = ""
	m.DesktopSend(p, desktop.Input{Kind: "text", Text: text})
}

func (m *App) DesktopPageView(c *ui.Context) {
	p := m.DesktopPage
	pal := m.Pal
	if p == nil {
		ui.Column(c).Fill().Background(pal.Bg)
		return
	}
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).FillWidth().MinHeight(56).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			m.navigationBack(c, "/desktop")
			title := tr("Remote desktop")
			if p.WindowOnly {
				title = textutil.FirstNonEmpty(p.Window.Title, p.Window.App, tr("Permission window"))
			}
			if !p.WindowOnly && p.Display.ID != 0 {
				title = p.displayTitle()
			}
			if p.Phase == "picker" {
				title = tr("Select window")
				if p.PickerTab == 1 {
					title = tr("Apps")
				}
				if p.PickerTab == 2 {
					title = tr("Full desktop")
				}
			}
			ui.Column(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Gap(2).Children(func() {
				ui.Text(c, title).TextAlign(ui.Center).FontSize(m.Font(16)).FontWeight(600).MaxLines(1)
				if p.Phase == "live" && p.Status != "" {
					ui.Text(c, p.Status).TextAlign(ui.Center).FontSize(m.Font(12)).TextColor(pal.muted).MaxLines(1)
				}
			})
			if p.Phase == "picker" && !p.Busy {
				if m.circle(c, "desktop-refresh-windows", tr("Refresh list"), func() { widgets.Icon(c, "refresh", 20, pal.ink) }).Clicked() {
					if p.PickerTab == 1 {
						m.DesktopChooseApps(p)
					} else if p.PickerTab == 2 {
						m.desktopChooseDisplays(p)
					} else {
						m.desktopChoose(p)
					}
				}
			}
			if p.Phase == "live" && m.circle(c, "desktop-minimize", tr("Show as floating window"), func() { widgets.Icon(c, "picture-in-picture", 20, pal.ink) }).Clicked() {
				c.Blur()
				m.DesktopMinimize(p)
			}
			if p.Phase == "picker" && !p.Busy {
				// The refresh button above takes the trailing slot.
			} else if (p.WindowOnly || p.Pickable) && (p.Phase == "live" || p.Phase == "error") {
				label := tr("Switch window")
				if !p.WindowOnly && p.DisplaysAvailable {
					label = tr("Switch desktop")
				}
				if m.circle(c, "desktop-choose", label, func() { widgets.Icon(c, "stack-2", 20, pal.ink) }).Clicked() {
					if !p.WindowOnly && p.DisplaysAvailable {
						m.desktopChooseDisplays(p)
					} else {
						m.desktopChoose(p)
					}
				}
			} else if p.Phase != "live" {
				// Balance the back button so the title stays centered.
				ui.Box(c).Size(44, 44).Shrink(0)
			}
		})
		switch p.Phase {
		case "live":
			m.desktopLiveView(c, p)
		case "picker":
			m.desktopPickerView(c, p)
		default:
			m.desktopStateView(c, p)
		}
	})
}

func (m *App) desktopStateView(c *ui.Context, p *MobileDesktopPage) {
	pal := m.Pal
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Padding(24).Gap(14).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		if p.Busy {
			ui.Spinner(c)
		}
		ui.Text(c, p.Title).FontSize(m.Font(20)).FontWeight(600).TextAlign(ui.Center)
		if p.Detail != "" {
			ui.Text(c, p.Detail).FontSize(m.Font(15)).TextColor(pal.muted).TextAlign(ui.Center).LineHeight(1.5)
		}
		if p.Phase == "error" || p.Phase == "paused" && !p.Busy {
			if m.pill(c, "desktop-retry", tr("Reconnect"), true).Clicked() {
				m.desktopRetry(p)
			}
		}
		if p.Phase == "error" || p.Phase == "ended" || p.Phase == "paused" {
			if m.pill(c, "desktop-return", tr("Back to conversation"), p.Phase == "ended").Clicked() {
				m.Router().Back()
			}
		}
	})
}

func (m *App) desktopPickerView(c *ui.Context, p *MobileDesktopPage) {
	pal := m.Pal
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(16).Gap(10).Children(func() {
			if p.Pickable && (p.AppsAvailable || p.DisplaysAvailable) {
				ui.Row(c).FillWidth().Gap(8).Children(func() {
					if p.DisplaysAvailable && m.pill(c, "desktop-tab-displays", tr("Desktops"), p.PickerTab == 2).Grow(1).Basis(0).MinWidth(0).Disabled(p.Busy).Clicked() {
						m.desktopChooseDisplays(p)
					}
					if m.pill(c, "desktop-tab-windows", tr("Windows"), p.PickerTab == 0).Grow(1).Basis(0).MinWidth(0).Disabled(p.Busy).Clicked() {
						m.desktopChoose(p)
					}
					if p.AppsAvailable && m.pill(c, "desktop-tab-apps", tr("Apps"), p.PickerTab == 1).Grow(1).Basis(0).MinWidth(0).Disabled(p.Busy).Clicked() {
						m.DesktopChooseApps(p)
					}
				})
			}
			if p.PickerTab == 2 {
				m.desktopDisplayList(c, p)
				return
			}
			if p.PickerTab == 1 {
				m.desktopApplicationList(c, p)
				return
			}
			if p.appStatus != "" {
				ui.Text(c, p.appStatus).FontSize(m.Font(14)).TextColor(pal.muted).LineHeight(1.5)
			}
			ui.Text(c, tr("Only the selected window is shared. Complete Touch ID or local confirmation on your Mac when required.")).FontSize(m.Font(14)).TextColor(pal.muted).LineHeight(1.5)
			if p.Busy {
				ui.Spinner(c).Margin(12)
				return
			}
			if len(p.Windows) == 0 {
				ui.Text(c, tr("No shareable windows.")).FontSize(m.Font(15)).TextColor(pal.muted)
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

func (m *App) desktopLiveView(c *ui.Context, p *MobileDesktopPage) {
	pal := m.Pal
	vp := ui.Column(c.Key("desktop-viewport")).FillWidth().Grow(1).MinHeight(0).Clip().Background(pal.Bg).Label(tr("Live Mac screen. Tap, swipe, or pinch to zoom."))
	if !p.Trackpad && p.TouchMode == 0 {
		vp.TouchScroll()
	}
	if b := vp.Bounds(); b.W > 0 && b.H > 0 {
		p.View = ui.Rect{W: b.W, H: b.H}
	}
	m.desktopGestures(p, vp.Gestures())
	vp.HandleInput(func(ev ui.InputEvent) bool { return m.DesktopPointerInput(p, ev) })
	r, haveImage := p.ImageRect()
	taps := p.ActiveTaps(time.Now())
	if len(taps) > 0 {
		c.After(16 * time.Millisecond) // animate until the ripples fade
	}
	if haveImage && (p.Trackpad || len(taps) > 0) {
		trackpad, cursor, dragging := p.Trackpad, p.Cursor, p.pointer.active && p.pointer.drag
		vp.DrawOver(func(painter *ui.Painter, box ui.Rect) {
			now := time.Now()
			for _, tap := range taps {
				t := float32(now.Sub(tap.when)) / float32(DesktopTapDuration)
				if t < 0 || t >= 1 {
					continue
				}
				x, y := box.X+r.X+float32(tap.At.X)*r.W, box.Y+r.Y+float32(tap.At.Y)*r.H
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
			painter.StrokePath(new(ui.Path).Circle(x, y, radius), 2, pal.Bg)
		})
	}
	p.overlays = p.overlays[:0]
	vp.Children(func() {
		if haveImage {
			ui.Image(c, p.Image).Absolute().Left(r.X).Top(r.Y).Width(r.W).Height(r.H).Fit(ui.FillBox)
		} else {
			ui.Column(c).Fill().Gap(10).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				if !p.Unlock.Locked {
					ui.Spinner(c)
				}
				ui.Text(c, p.Status).FontSize(m.Font(14)).TextColor(pal.muted).TextAlign(ui.Center).MaxLines(3)
			})
		}
		if !p.Unlock.Locked {
			m.desktopControls(c, p)
		}
	})
	hasActions := p.WindowOnly && !p.actionsUnsupported && len(p.actions.Actions) > 0
	unlock := p.canUnlock() && (p.Unlock.Locked || p.Unlock.Status != "")
	typing := !p.Unlock.Locked && p.keyboard
	if !hasActions && !unlock && !typing && p.actionStatus == "" {
		return // the screen keeps the whole page
	}
	ui.Column(c).FillWidth().Padding(8, 12, 8, 12).Gap(8).Children(func() {
		m.desktopUnlockView(c, p)
		if hasActions {
			ui.Row(c).FillWidth().Wrap().Justify(ui.End).Gap(8).Children(func() {
				for _, a := range p.actions.Actions {
					primary := a.Default && !a.Cancel
					if m.ApprovalButton(c, "desktop-action-"+a.ID, a.Label, primary, !a.Enabled || !p.actionsFresh || p.actionPending).Clicked() {
						m.desktopPressAction(p, a)
					}
				}
			})
		}
		if p.actionStatus != "" {
			ui.Text(c, p.actionStatus).FontSize(m.Font(12)).TextColor(pal.muted).MaxLines(2)
		}
		if typing {
			m.desktopInputBar(c, p)
		}
	})
}

// desktopControls floats a small capsule over the bottom of the screen, as
// remote desktop apps do; the options panel opens above it.
func (m *App) desktopControls(c *ui.Context, p *MobileDesktopPage) {
	if p.View.W == 0 {
		return
	}
	pal := m.Pal
	hasMore := !p.Trackpad || (p.canUnlock() && p.Unlock.Saved) || (p.Pickable && p.Privacy.Available)
	if !hasMore {
		p.More = false
	}
	n := float32(2)
	if hasMore {
		n = 3
	}
	if p.Pickable && p.AppsAvailable {
		n++
	}
	const size, gap, pad = 44, 4, 4
	w, h := n*size+(n-1)*gap+2*pad, float32(size+2*pad)
	x, y := (p.View.W-w)/2, p.View.H-h-16
	p.overlays = append(p.overlays, ui.Rect{X: x, Y: y, W: w, H: h})
	if p.More {
		pw := min(p.View.W-24, 340)
		m.desktopMorePanel(c, p, (p.View.W-pw)/2, p.View.H-y+10, pw)
	}
	ui.Row(c.Key("desktop-controls")).Absolute().Left(x).Top(y).Width(w).Height(h).Padding(pad).Gap(gap).AlignItems(ui.Center).
		Radius(h/2).Background(pal.surface).Border(1, pal.line).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.22)).Children(func() {
		if p.Pickable && p.AppsAvailable && m.desktopControl(c, "desktop-apps", "apps", tr("Open app"), false).Clicked() {
			c.Blur()
			m.DesktopChooseApps(p)
		}
		if m.desktopControl(c, "desktop-keyboard", "keyboard", tr("Keyboard"), p.keyboard).Clicked() {
			p.keyboard, p.More, p.moreKeys = !p.keyboard, false, false
			p.focusText = p.keyboard
			if !p.keyboard {
				c.Blur()
			}
		}
		label, glyph := tr("Trackpad"), "pointer"
		if !p.Trackpad {
			label, glyph = tr("Touch"), "hand-stop"
		}
		if m.desktopControl(c, "desktop-pointer", glyph, tr("Switch control mode, currently ")+label, false).Clicked() {
			p.Trackpad, p.pointer = !p.Trackpad, desktopPointer{}
			prefs := map[bool]string{true: "trackpad", false: "touch"}[p.Trackpad]
			path := m.desktopPrefsFile()
			go func() { _ = control.WritePrefs(path, prefs) }()
		}
		if hasMore && m.desktopControl(c, "desktop-more-options", "dots", tr("More options"), p.More).Clicked() {
			p.More = !p.More
		}
	})
}

// desktopControl is a round icon button in the floating capsule.
func (m *App) desktopControl(c *ui.Context, key, glyph, label string, on bool) ui.Element {
	fg := m.Pal.ink
	b := m.phoneButton(c.Key(key)).Size(44, 44).Padding(0).Radius(22).Justify(ui.Center).Label(label)
	if on {
		b.Background(m.Pal.ink)
		fg = m.Pal.Bg
	}
	b.Children(func() { widgets.Icon(c, glyph, 22, fg) })
	return b
}

// desktopMorePanel holds the occasional settings: how touches act in touch
// mode and the saved login password.
func (m *App) desktopMorePanel(c *ui.Context, p *MobileDesktopPage, left, bottom, width float32) {
	pal := m.Pal
	panel := ui.Column(c.Key("desktop-more-panel")).Absolute().Left(left).Bottom(bottom).Width(width).Padding(12).Gap(12).
		Radius(18).Background(pal.surface).Border(1, pal.line).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.22))
	if b := panel.Bounds(); b.H > 0 {
		p.overlays = append(p.overlays, ui.Rect{X: left, Y: p.View.H - bottom - b.H, W: width, H: b.H})
	}
	panel.Children(func() {
		ui.Scroll(c).FillWidth().MaxHeight(max(44, p.View.H-bottom-36)).Children(func() {
			ui.Column(c).FillWidth().Gap(12).Children(func() {
				m.desktopPrivacyView(c, p)
				if !p.Trackpad {
					ui.Segmented(c.Key("desktop-touch-mode"), &p.TouchMode, tr("Click / Scroll"), tr("Drag"), tr("Move screen")).FillWidth()
				}
				if p.canUnlock() && p.Unlock.Saved {
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						widgets.Icon(c, "shield-check", 18, pal.ok)
						ui.Text(c, tr("Password saved for this Mac")).Grow(1).MinWidth(0).FontSize(m.Font(14)).MaxLines(1)
						forget := m.phoneButton(c.Key("desktop-forget-saved")).PaddingX(8).Justify(ui.Center).Label(tr("Forget password")).Disabled(p.Unlock.Busy)
						forget.Children(func() {
							ui.Text(c, tr("Forget password")).FontSize(m.Font(14)).FontWeight(500).TextColor(pal.warn)
						})
						if forget.Clicked() {
							p.More = false
							m.DesktopForgetUnlock(p)
						}
					})
				}
			})
		})
	})
}

// desktopInputBar is a message-style capsule: the lock toggles password
// entry and the round arrow sends. Keys ride above the system keyboard.
func (m *App) desktopInputBar(c *ui.Context, p *MobileDesktopPage) {
	pal := m.Pal
	svc := c.Services()
	handler := func(id string) { m.desktopAccessory(p, svc, id) }
	focused := false
	ui.Row(c.Key("desktop-input")).FillWidth().MinHeight(46).Padding(0, 2).Gap(2).AlignItems(ui.Center).Background(pal.surface).Radius(23).Children(func() {
		// A secure field forces iOS's ASCII keyboard without input methods,
		// so hide the text only when the person chooses password entry.
		mode, other, glyph := tr("Text"), tr("Password"), "lock-open"
		if p.Secret {
			mode, other, glyph = other, mode, "lock"
		}
		secret := m.phoneButton(c.Key("desktop-secret")).Size(44, 44).Padding(0).Radius(22).Justify(ui.Center).KeepFocus().
			Label(tr("Current: ") + mode + tr(" input, switch to ") + other + tr(" input"))
		secret.Children(func() {
			color := pal.muted
			if p.Secret {
				color = pal.ink
			}
			widgets.Icon(c, glyph, 20, color)
		})
		if secret.Clicked() {
			p.Secret = !p.Secret
		}
		field := ui.TextInput(c.Key("desktop-text"), &p.text).Grow(1).MinWidth(0).MinHeight(44).Padding(10, 4).Background(pal.surface).Border(1, pal.surface).
			Autocorrect(false).Capitalize(ui.CapitalizeNone).ReturnKey(ui.ReturnGo)
		if p.Secret {
			field.Password().Label(tr("Password to send to Mac")).Placeholder(tr("Enter password"))
		} else {
			field.Label(tr("Text to send to Mac")).Placeholder(tr("Enter text to send"))
		}
		focused = field.Focused()
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
				widgets.Icon(c, "arrow-up", 18, pal.Bg)
			})
		})
		if send.Clicked() {
			m.desktopSendText(p)
		}
	})
	// The keys show while the field has the focus, at the bottom of the page,
	// which phones lay out above the software keyboard.
	if focused {
		keyboardbar.Bar(c, p.accessoryActions(), &p.moreKeys, handler)
	}
}

// accessoryActions are the Mac keys above the software keyboard: the common
// four, a panel of modifiers, arrows and shortcut letters, and hide.
func (p *MobileDesktopPage) accessoryActions() []keyboardbar.Action {
	var actions []keyboardbar.Action
	for _, k := range desktopKeys {
		actions = append(actions, keyboardbar.Action{ID: "key-" + k.key, Label: k.label})
	}
	more := keyboardbar.Action{ID: "more", Label: tr("More")}
	for _, k := range desktopModifiers {
		more.Items = append(more.Items, keyboardbar.Action{ID: "mod-" + k.key, Label: k.label, Selected: p.modifier == k.key})
	}
	for _, keys := range [][]desktopKeyDef{desktopArrowKeys, desktopLetterKeys} {
		for _, k := range keys {
			more.Items = append(more.Items, keyboardbar.Action{ID: "key-" + k.key, Label: k.label})
		}
	}
	return append(actions, more, keyboardbar.Action{ID: "hide", Label: tr("Hide keyboard")})
}

func (m *App) desktopAccessory(p *MobileDesktopPage, svc ui.Services, id string) {
	if m.DesktopPage != p {
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
	uiruntime.Invalidate()
}

// desktopSelectFull switches a menu lease to the full desktop.
func (m *App) desktopSelectFull(p *MobileDesktopPage) {
	if !p.Pickable {
		return
	}
	if p.DisplaysAvailable {
		m.desktopChooseDisplays(p)
		return
	}
	p.InvalidateTarget()
	m.DesktopStage(p, "connecting", tr("Connecting to Mac"), tr("Switching to full desktop."), true)
	m.desktopDo(p, func(ctx context.Context) func() {
		err := p.Client.SelectWindow(ctx, desktop.Window{})
		return func() {
			if err != nil {
				m.desktopFail(p, tr("Unable to switch"), err)
				return
			}
			p.Window, p.WindowOnly, p.Zoom, p.PanX, p.panY = desktop.Window{}, false, 1, 0, 0
			p.actions, p.actionsFresh = control.Actions{}, false
			m.desktopStart(p)
		}
	})
}

var SendDesktopClose = func(conn *remote.Conn, token string) {
	go func() { _ = conn.Send(remote.Request{Op: "desktop-close", DesktopToken: token}) }()
}

// desktopRelease also revokes the lease on the control connection, besides
// /stop on the files stream. A reopen first resends these closes on the same
// connection, so the Mac never handles the new open while the old lease lives.
func (m *App) desktopRelease(p *MobileDesktopPage) {
	if p.released || p.Conn == nil {
		return
	}
	p.released = true
	if p.Conn == m.Conn {
		m.DesktopClosing = append(m.DesktopClosing, p.Client.Token())
	}
	SendDesktopClose(p.Conn, p.Client.Token())
}

func desktopWindowKey(w desktop.Window) string { return fmt.Sprintf("%d-%d", w.ID, w.PID) }

// desktopLoadThumbs fetches picker previews one at a time.
func (m *App) desktopLoadThumbs(p *MobileDesktopPage) {
	if !p.Pickable || p.ThumbBusy || p.Phase != "picker" || p.PickerTab != 0 || p.Busy {
		return
	}
	for _, w := range p.Windows {
		key := desktopWindowKey(w)
		if _, done := p.Thumbs[key]; done {
			continue
		}
		p.ThumbBusy = true
		m.desktopDo(p, func(ctx context.Context) func() {
			data, err := p.Client.Thumbnail(ctx, w)
			var img ui.ImageSource
			if err == nil {
				img, _ = ui.DecodeImage(data)
			}
			return func() {
				p.ThumbBusy = false
				if control.Status(err) == 410 {
					m.DesktopFinish(p, err.Error())
					return
				}
				if p.Thumbs == nil {
					p.Thumbs = map[string]ui.ImageSource{}
				}
				p.Thumbs[key] = img
				m.desktopLoadThumbs(p)
			}
		})
		return
	}
}

// desktopWindowGrid shows the full desktop, when offered, and the shareable
// windows as preview cards two to a row.
func (m *App) desktopWindowGrid(c *ui.Context, p *MobileDesktopPage) {
	var cards []func()
	if p.Pickable && !p.DisplaysAvailable {
		cards = append(cards, func() { m.desktopFullCard(c, p) })
	}
	for _, w := range p.Windows {
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

func (m *App) desktopFullCard(c *ui.Context, p *MobileDesktopPage) {
	pal := m.Pal
	card := m.phoneButton(c.Key("desktop-full")).Column().Grow(1).Basis(0).MinWidth(0).Gap(6).Padding(8).Background(pal.surface).Radius(12).Label(tr("Full desktop"))
	card.Children(func() {
		ui.Column(c).FillWidth().AspectRatio(1.6).Radius(8).Background(pal.Bg).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			widgets.Icon(c, "device-desktop", 32, pal.muted)
		})
		ui.Text(c, tr("Full desktop")).FontSize(m.Font(14)).FontWeight(600).LineHeight(1.3).MaxLines(2)
	})
	if card.Clicked() {
		m.desktopSelectFull(p)
	}
}

func (m *App) desktopWindowCard(c *ui.Context, p *MobileDesktopPage, w desktop.Window) {
	pal := m.Pal
	key := desktopWindowKey(w)
	img, loaded := p.Thumbs[key]
	title := textutil.FirstNonEmpty(w.Title, w.App, tr("Untitled window"))
	card := m.phoneButton(c.Key("desktop-window-" + key)).Column().Grow(1).Basis(0).MinWidth(0).Gap(6).Padding(8).Background(pal.surface).Radius(12)
	card.Children(func() {
		ui.Column(c).FillWidth().AspectRatio(1.6).Radius(8).Clip().Background(pal.Bg).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			switch {
			case img != nil:
				ui.Image(c, img).Fill().Fit(ui.Contain)
			case !loaded && p.Pickable:
				ui.Spinner(c)
			default:
				ui.Text(c, textutil.FirstNonEmpty(w.App, tr("Window"))).FontSize(m.Font(12)).TextColor(pal.muted).MaxLines(1)
			}
		})
		ui.Text(c, title).FontSize(m.Font(14)).FontWeight(600).LineHeight(1.3).MaxLines(2)
		if w.Title != "" && w.App != "" {
			ui.Text(c, w.App).FontSize(m.Font(12)).TextColor(pal.muted).MaxLines(1)
		}
	})
	if card.Clicked() {
		m.desktopSelect(p, w)
	}
}

type desktopTap struct {
	At   DesktopPoint
	when time.Time
}

const DesktopTapDuration = 450 * time.Millisecond

// markTap remembers where a queued click or drag landed for its ripple.
func (p *MobileDesktopPage) markTap(in desktop.Input, now time.Time) {
	switch in.Kind {
	case "click", "doubleClick", "rightClick":
		p.Taps = append(p.ActiveTaps(now), desktopTap{DesktopPoint{in.X, in.Y}, now})
	case "drag":
		p.Taps = append(p.ActiveTaps(now), desktopTap{DesktopPoint{in.X, in.Y}, now}, desktopTap{DesktopPoint{in.ToX, in.ToY}, now})
	default:
		return
	}
	if n := len(p.Taps); n > 4 {
		p.Taps = p.Taps[n-4:]
	}
}

func (p *MobileDesktopPage) ActiveTaps(now time.Time) []desktopTap {
	keep := p.Taps[:0]
	for _, tap := range p.Taps {
		if now.Sub(tap.when) < DesktopTapDuration {
			keep = append(keep, tap)
		}
	}
	p.Taps = keep
	return keep
}
