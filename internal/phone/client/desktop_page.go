package client

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"time"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/phone/desktopview"
	"miya/internal/remote"
	"miya/internal/textutil"
)

type DesktopPoint = desktopview.Point

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
	Image           desktopview.ImageSource
	ImageHash       [32]byte
	Status          string

	Zoom, PanX, panY float32
	View             desktopview.Rect
	Trackpad         bool
	TouchMode        int // 0 tap and swipe, 1 drag, 2 move the zoomed view
	Cursor           DesktopPoint
	Mapping          desktopview.Mapper

	Text     string
	Modifier string
	MoreKeys bool
	// Keyboard shows the Text field and keys; otherwise the screen gets the room.
	Keyboard bool
	More     bool
	// secret hides typed Text; authorization windows start with it on.
	Secret bool
	// pickable leases came from the menu and may choose any window.
	Pickable             bool
	AppsAvailable        bool
	PickerTab            int // 0 windows, 1 applications
	Apps                 []desktop.Application
	AppIcons             map[string]desktopview.ImageSource
	AppSearch, AppStatus string
	DisplaysAvailable    bool
	Displays             []desktop.Display
	Display              desktop.Display
	targetRevision       int
	// thumbs holds picker previews by window; nil marks one that failed.
	Thumbs    map[string]desktopview.ImageSource
	ThumbBusy bool

	Queue   []desktop.Input
	Sending bool

	Actions                                  control.Actions
	ActionsFresh, actionsBusy, ActionPending bool
	actionsUnsupported                       bool
	ActionStatus                             string

	Unlock  DesktopUnlockState
	Privacy mobileDesktopPrivacy

	// Floating keeps the session in an in-app floating window while other
	// pages show; leaving the page then does not end control.
	Floating       bool
	FloatX, FloatY float32
}

func (m *Client) desktopPrefsFile() string {
	if m.DataDir == "" {
		return ""
	}
	return filepath.Join(m.DataDir, "desktop-control.json")
}

func (m *Client) desktopUIOpen() bool { return m.DesktopPage != nil }

func (m *Client) newDesktopPage(conn *remote.Conn, token string) *MobileDesktopPage {
	ctx, cancel := context.WithCancel(context.Background())
	var client *control.Client
	if conn != nil {
		client = control.NewClient(conn.DialFiles, token)
	}
	if m.DesktopClient != nil {
		client = m.DesktopClient(conn, token)
	}
	p := &MobileDesktopPage{Client: client, Conn: conn, Ctx: ctx, Cancel: cancel, gen: m.Generation,
		Zoom: 1, Cursor: DesktopPoint{X: .5, Y: .5}, Trackpad: control.ReadPrefs(m.desktopPrefsFile()).PointerMode != "touch"}
	p.Unlock.Key = DesktopUnlockKey(m.CurrentDesktopID())
	p.owner, p.desktopLink = m.desktopOpening, m.Link
	return p
}

func (m *Client) openDesktopPage(conn *remote.Conn, token string) {
	p := m.newDesktopPage(conn, token)
	m.DesktopPage = p
	m.DesktopReturn = m.Path
	m.Path = "/desktop"
	m.DesktopInitialize(p)
}

// closeDesktopPage revokes the lease and forgets the page. back also leaves
// the page, as when the approval it served has been resolved.
func (m *Client) CloseDesktopPage(back bool) {
	p := m.DesktopPage
	if p == nil {
		return
	}
	m.DesktopPage = nil
	p.Active = false
	p.Mapping.Reset()
	if p.resuming {
		m.stopDesktopOpen()
	}
	m.DesktopApproval = MobileDesktopApproval{}
	m.desktopRelease(p)
	if p.Phase != "ended" {
		go p.Client.Stop()
	}
	if p.Cancel != nil {
		p.Cancel()
	}
	p.Image, p.Queue, p.Text, p.Unlock.Text = nil, nil, "", ""
	if back && m.Path == "/desktop" {
		m.Path = m.DesktopReturn
	}
}

// desktopDo runs work off the main thread and applies its result only while
// the same page is still open and has not ended.
func (m *Client) desktopDo(p *MobileDesktopPage, work func(ctx context.Context) func()) {
	ctx, revision := p.Ctx, p.targetRevision
	go func() {
		apply := work(ctx)
		// A closed page queues nothing more on the main thread.
		if ctx.Err() != nil {
			return
		}
		m.onMain(p.gen, func() {
			if ctx.Err() != nil || m.DesktopPage != p || p.Phase == "ended" || revision != p.targetRevision {
				return
			}
			apply()
			m.change()
		})
	}()
}

func (m *Client) desktopAfter(p *MobileDesktopPage, d time.Duration, f func()) {
	ctx := p.Ctx
	time.AfterFunc(d, func() {
		if ctx.Err() != nil {
			return
		}
		m.onMain(p.gen, func() {
			if ctx.Err() == nil && m.DesktopPage == p && p.Phase != "ended" {
				f()
				m.change()
			}
		})
	})
}

func (m *Client) DesktopStage(p *MobileDesktopPage, phase, title, detail string, busy bool) {
	p.Phase, p.Title, p.Detail, p.Busy = phase, title, detail, busy
}

func (m *Client) desktopFail(p *MobileDesktopPage, title string, err error) {
	if control.Status(err) == 410 {
		m.DesktopFinish(p, err.Error())
		return
	}
	p.Active = false
	m.DesktopStage(p, "error", title, err.Error(), false)
}

// desktopFinish keeps the page with its reason; the lease is already gone
// or is revoked here, and the last frame and any typed Text are dropped.
func (m *Client) DesktopFinish(p *MobileDesktopPage, Message string) {
	if p.Phase == "ended" {
		return
	}
	p.suspended, p.resuming, p.restoring = false, false, false
	p.Active, p.Image, p.Queue, p.Text, p.Unlock.Text = false, nil, nil, "", ""
	p.Mapping.Reset()
	m.DesktopStage(p, "ended", tr("Control session ended"), Message, false)
	if p.Cancel != nil {
		p.Cancel()
	}
	go p.Client.Stop()
	m.desktopRelease(p)
	m.change()
}

func (m *Client) DesktopInitialize(p *MobileDesktopPage) {
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
				m.DesktopChooseWindows(p)
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
			p.Keyboard = true // authorization windows usually need a password
			if err != nil {
				m.desktopFail(p, tr("Connection incomplete"), err)
				return
			}
			p.Windows = windows
			for _, w := range windows {
				if w.ID == want.ID && w.PID == want.PID {
					m.DesktopSelectWindow(p, w)
					return
				}
			}
			m.DesktopFinish(p, tr("The permission window closed. Return to the conversation to view the result."))
		}
	})
}

func (m *Client) DesktopChooseWindows(p *MobileDesktopPage) {
	// Switching revokes input: the next window needs its own frame first.
	p.InvalidateTarget()
	p.PickerTab, p.AppStatus = 0, ""
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

func (m *Client) DesktopSelectWindow(p *MobileDesktopPage, w desktop.Window) {
	same := p.WindowOnly && p.Window.ID == w.ID && p.Window.PID == w.PID
	p.InvalidateTarget()
	m.DesktopStage(p, "connecting", tr("Opening window"), tr("Checking the connection and system permissions."), true)
	m.desktopDo(p, func(ctx context.Context) func() {
		err := p.Client.SelectWindow(ctx, w)
		return func() {
			if err != nil {
				m.desktopFail(p, tr("Unable to open window"), err)
				return
			}
			p.Window, p.WindowOnly = w, true
			if !same {
				p.Zoom, p.PanX, p.panY = 1, 0, 0
			}
			p.Display = desktop.Display{}
			m.desktopStart(p)
		}
	})
}

func (m *Client) desktopStart(p *MobileDesktopPage) {
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

func (m *Client) DesktopRetry(p *MobileDesktopPage) {
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
func (m *Client) DesktopPoll(p *MobileDesktopPage) {
	if !p.Active || p.Polling {
		return
	}
	p.Polling = true
	revision := p.targetRevision
	previous, hash := p.Image, p.ImageHash
	m.desktopDo(p, func(ctx context.Context) func() {
		data, locked, err := p.Client.Frame(ctx)
		var image desktopview.ImageSource
		if err == nil {
			if previous != nil && sha256.Sum256(data) == hash {
				image = previous
			} else {
				image, _ = desktopview.DecodeImage(data)
			}
			if image == nil {
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

func (m *Client) ReceiveDesktopFrame(p *MobileDesktopPage, data []byte, image desktopview.ImageSource, locked bool, err error) {
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
		m.DesktopChooseDisplays(p)
		p.AppStatus = tr("The display changed or disconnected. Select a desktop.")
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

func (m *Client) desktopRefreshActions(p *MobileDesktopPage) {
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
				p.Actions, p.ActionsFresh = a, true
			case status == 410:
				m.DesktopFinish(p, err.Error())
				return
			case status == 404 || status == 403:
				p.actionsUnsupported, p.ActionsFresh = true, false
				return
			default:
				p.ActionsFresh = false
				p.ActionStatus = err.Error()
			}
			m.desktopAfter(p, 2*time.Second, func() { m.desktopRefreshActions(p) })
		}
	})
}

func (m *Client) DesktopPressAction(p *MobileDesktopPage, a desktop.Action) {
	if p.ActionPending || !p.ActionsFresh || !a.Enabled || !p.Active {
		return
	}
	p.ActionPending, p.ActionStatus = true, tr("Checking permission result…")
	w := p.Actions.Window
	m.desktopDo(p, func(ctx context.Context) func() {
		err := p.Client.PressAction(ctx, a.ID, w)
		return func() {
			p.ActionPending, p.ActionsFresh = false, false
			if control.Status(err) == 410 {
				m.DesktopFinish(p, err.Error())
				return
			}
			if err != nil {
				p.ActionStatus = tr("Permission result unconfirmed. Check the window state and retry.") + err.Error()
			} else {
				p.ActionStatus = tr("Check the window instructions. If permission is still pending, retry here.")
			}
			m.desktopRefreshActions(p)
		}
	})
}

// desktopSend keeps input in order. Consecutive moves keep the latest;
// consecutive scrolls add up, so a fast swipe cannot flood the Mac.
func (m *Client) DesktopSend(p *MobileDesktopPage, in desktop.Input) {
	if p.Floating || p.Phase != "live" {
		return
	}
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
			last.DeltaX = max(-1000, min(1000, last.DeltaX+in.DeltaX))
			last.DeltaY = max(-1000, min(1000, last.DeltaY+in.DeltaY))
			last.X, last.Y = in.X, in.Y
			return
		}
		if n >= 64 {
			p.Status = tr("Too many actions. Please wait.")
			return
		}
	}
	p.Queue = append(p.Queue, in)

	m.desktopPump(p)
}

func (m *Client) desktopPump(p *MobileDesktopPage) {
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
				p.ActionsFresh = false
				m.desktopRefreshActions(p)
			}
			m.desktopAfter(p, 30*time.Millisecond, func() { m.desktopPump(p) })
		}
	})
}

func (m *Client) sendDesktopClose(conn *remote.Conn, token string) {
	go m.write(conn, remote.Request{Op: "desktop-close", DesktopToken: token})
}

// desktopRelease also revokes the lease on the control connection, besides
// /stop on the files stream. A reopen first resends these closes on the same
// connection, so the Mac never handles the new open while the old lease lives.
func (m *Client) desktopRelease(p *MobileDesktopPage) {
	if p.released || p.Client == nil {
		return
	}
	p.released = true
	if p.Conn == m.Conn {
		m.DesktopClosing = append(m.DesktopClosing, p.Client.Token())
	}
	m.sendDesktopClose(p.Conn, p.Client.Token())
}

func desktopWindowKey(w desktop.Window) string { return fmt.Sprintf("%d-%d", w.ID, w.PID) }

// desktopLoadThumbs fetches picker previews one at a time.
func (m *Client) desktopLoadThumbs(p *MobileDesktopPage) {
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
			var img desktopview.ImageSource
			if err == nil {
				img, _ = desktopview.DecodeImage(data)
			}
			return func() {
				p.ThumbBusy = false
				if control.Status(err) == 410 {
					m.DesktopFinish(p, err.Error())
					return
				}
				if p.Thumbs == nil {
					p.Thumbs = map[string]desktopview.ImageSource{}
				}
				p.Thumbs[key] = img
				m.desktopLoadThumbs(p)
			}
		})
		return
	}
}

func (m *Client) desktopLeaseDo(p *MobileDesktopPage, work func(context.Context) func()) {
	ctx := p.Ctx
	go func() {
		apply := work(ctx)
		if ctx.Err() != nil {
			return
		}
		m.onMain(p.gen, func() {
			if ctx.Err() == nil && m.DesktopPage == p && p.Phase != "ended" {
				apply()
			}
		})
	}()
}
