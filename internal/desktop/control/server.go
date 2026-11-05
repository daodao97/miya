// Package control lets a paired phone see and operate this Mac: the HTTP
// server that leases the desktop or one window to the phone, and the client
// the phone uses.
package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"miya/internal/desktop"
)

const Prefix = "/.miya-desktop/v1"
const desktopControlTTL = 10 * time.Minute

type desktopLease struct {
	token         string
	ctx           context.Context
	active        bool
	expires       time.Time
	lastFrame     time.Time
	capturedAt    time.Time
	lastInput     time.Time
	stop          func() bool
	window        *desktop.Window // nil means explicitly requested full desktop
	onAction      func(desktop.Window, desktop.Action, []desktop.Window)
	actionPending bool
	lastWake      time.Time
	// pick lets a lease opened from the phone menu choose any window or the
	// full desktop. Authorization leases stay bound to their window.
	pick                 bool
	unlocking            bool
	launching            bool
	privacy              desktop.Privacy
	display              *desktop.Display
	displayInputRequired bool
}

// One operator at a time. A lease belongs to one live phone control
// connection and expires even if a client keeps polling forever.
// desktopNative is what remote control needs from the operating system:
// internal/desktop in the app, fakes in tests.
type Native struct {
	Authorize      func() error
	Capture        func() ([]byte, error)
	Input          func(desktop.Input) error
	Windows        func() ([]desktop.Window, error)
	Apps           func() ([]desktop.Application, error)
	OpenApp        func(context.Context, string) error
	Privacy        func(context.Context) (desktop.Privacy, error)
	Displays       func() ([]desktop.Display, error)
	CaptureDisplay func(desktop.Display) ([]byte, error)
	InputDisplay   func(desktop.Display, desktop.Input) error
	CaptureWindow  func(desktop.Window) ([]byte, error)
	InputWindow    func(desktop.Window, desktop.Input) error
	Dialog         func(desktop.Window) (desktop.Dialog, error)
	PressAction    func(desktop.Window, string) error
	Wake           func() error
	// locked reports the console lock screen; nil disables remote unlock.
	Locked func() (bool, error)
}

type Server struct {
	mu          sync.Mutex
	work        sync.Mutex
	privacyWork sync.Mutex
	lease       *desktopLease
	Native
}

func (d *Server) Open(ctx context.Context) (string, error) {
	return d.OpenWindow(ctx, nil)
}

func (d *Server) OpenWindow(ctx context.Context, window *desktop.Window) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if old := d.lease; old != nil && old.ctx.Err() == nil && time.Now().Before(old.expires) {
		return "", errors.New("Another remote desktop view is still open. End that control session first.")
	}
	if old := d.lease; old != nil {
		old.stop()
	}
	leaseCtx, cancel := context.WithTimeout(ctx, desktopControlTTL)
	l := &desktopLease{token: hex.EncodeToString(raw[:]), ctx: leaseCtx, expires: time.Now().Add(desktopControlTTL)}
	if window != nil {
		copy := *window
		l.window = &copy
	}
	l.stop = context.AfterFunc(leaseCtx, func() { d.Revoke(l.token) })
	// Release the deadline timer as well as the session when manually closed.
	stop := l.stop
	l.stop = func() bool { result := stop(); cancel(); return result }
	d.lease = l
	return l.token, nil
}

// keepAwake lights a sleeping display before capture and keeps it lit while
// an operator polls. It only declares user activity; once polling stops the
// Mac's own idle sleep resumes. A failure surfaces as the capture error.
func (d *Server) keepAwake(l *desktopLease, force bool) {
	if d.Wake == nil {
		return
	}
	d.mu.Lock()
	due := force || time.Since(l.lastWake) >= 5*time.Second
	if due {
		l.lastWake = time.Now()
	}
	d.mu.Unlock()
	if due {
		_ = d.Wake()
	}
}

// allowPick lets a lease opened from the phone menu choose any shareable
// window or the full desktop. Authorization leases never get it.
func (d *Server) AllowPick(token string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if l := d.valid(token); l != nil {
		l.pick = true
	}
}

func (d *Server) Revoke(token string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lease != nil && d.lease.token == token {
		l := d.lease
		d.lease = nil
		l.stop()
		if l.privacy != nil {
			go l.privacy.Close()
		}
	}
}

func (d *Server) valid(token string) *desktopLease {
	l := d.lease
	if token == "" || l == nil || l.token != token || l.ctx.Err() != nil || !time.Now().Before(l.expires) {
		return nil
	}
	return l
}

func (d *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	d.mu.Lock()
	l := d.valid(token)
	d.mu.Unlock()
	if l == nil {
		http.Error(w, "Remote control ended or disconnected. Return to the conversation to reopen it.", http.StatusGone)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, Prefix)
	if path == "/stop" && r.Method == http.MethodPost {
		d.Revoke(token)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if path == "/session" && r.Method == http.MethodGet {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.valid(token) != l {
			http.Error(w, "Remote control ended", 410)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			WindowOnly bool             `json:"windowOnly"`
			Window     *desktop.Window  `json:"window"`
			Pick       bool             `json:"pick"`
			Apps       bool             `json:"apps"`
			Privacy    bool             `json:"privacy"`
			Displays   bool             `json:"displays"`
			Display    *desktop.Display `json:"display,omitempty"`
		}{l.window != nil, l.window, l.pick, l.pick && d.Apps != nil && d.OpenApp != nil, l.pick && d.Privacy != nil, l.pick && d.displaysAvailable(), l.display})
		return
	}
	if path == "/actions" || path == "/action" {
		d.serveWindowActions(w, r, token, l, path == "/action")
		return
	}
	if path == "/apps" || path == "/launch" {
		d.serveApplications(w, r, token, l, path == "/launch")
		return
	}
	if path == "/privacy" {
		d.servePrivacy(w, r, token, l)
		return
	}
	if path == "/displays" || path == "/display" {
		d.serveDisplays(w, r, token, l, path == "/display")
		return
	}
	if path != "/frame" && path != "/start" && path != "/input" && path != "/windows" && path != "/select" && path != "/thumbnail" && path != "/unlock" {
		http.NotFound(w, r)
		return
	}
	get := path == "/frame" || path == "/windows" || path == "/thumbnail"
	if (get && r.Method != http.MethodGet) || (!get && r.Method != http.MethodPost) {
		http.Error(w, "Method not allowed", 405)
		return
	}
	// Image capture may wait on ScreenCaptureKit. Keep clicks and typing on
	// their own path so a slow frame never holds the input channel hostage.
	if path != "/input" && path != "/unlock" {
		if !d.work.TryLock() {
			http.Error(w, "The desktop is busy. Try again later.", http.StatusTooManyRequests)
			return
		}
		defer d.work.Unlock()
	}
	d.mu.Lock()
	if d.valid(token) != l {
		d.mu.Unlock()
		http.Error(w, "Remote control ended", 410)
		return
	}
	active := l.active
	pick := l.pick
	var display *desktop.Display
	if l.display != nil {
		copy := *l.display
		display = &copy
	}
	var window *desktop.Window
	if l.window != nil {
		copy := *l.window
		window = &copy
	}
	d.mu.Unlock()
	if path == "/thumbnail" {
		d.serveThumbnail(w, r, l, pick)
		return
	}
	if path == "/windows" || path == "/select" {
		if window == nil && !pick {
			http.Error(w, "Window control is not enabled in this view", 400)
			return
		}
		windows, err := d.Windows()
		if err != nil {
			http.Error(w, err.Error(), 403)
			return
		}
		if path == "/windows" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(windows)
			return
		}
		var selected desktop.Window
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&selected) != nil {
			http.Error(w, "Invalid window", 400)
			return
		}
		// Window 0 is the full desktop, offered only to menu leases.
		if pick && selected.ID == 0 && selected.PID == 0 {
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.valid(token) != l {
				http.Error(w, "Remote control ended", 410)
				return
			}
			if l.unlocking {
				http.Error(w, "Unlocking Mac. Please wait.", 409)
				return
			}
			l.window = nil
			l.display, l.displayInputRequired = nil, false
			l.active = false
			l.capturedAt = time.Time{}
			l.lastFrame = time.Time{}
			w.WriteHeader(204)
			return
		}
		for _, candidate := range windows {
			if candidate.ID != selected.ID || candidate.PID != selected.PID {
				continue
			}
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.valid(token) != l {
				http.Error(w, "Remote control ended", 410)
				return
			}
			if l.actionPending || l.unlocking {
				http.Error(w, "Checking the authorization result. Please wait.", 409)
				return
			}
			l.window = &candidate
			l.display, l.displayInputRequired = nil, false
			l.active = false
			l.capturedAt = time.Time{}
			l.lastFrame = time.Time{}
			w.WriteHeader(204)
			return
		}
		http.Error(w, "The window closed. Refresh the list.", 404)
		return
	}
	if path == "/start" {
		if err := d.Authorize(); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		d.keepAwake(l, true)
		if window != nil && window.ID == 0 {
			http.Error(w, "Select a target window first", 409)
			return
		}
		if window == nil && d.displaysAvailable() {
			selected, err := d.resolveDisplay(display)
			if err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
			display = &selected
		}
		d.mu.Lock()
		if d.valid(token) != l {
			d.mu.Unlock()
			http.Error(w, "Remote control ended", 410)
			return
		}
		l.active = true
		l.display = display
		d.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !active {
		http.Error(w, "Confirm that you want to start remote control first", http.StatusForbidden)
		return
	}
	if path == "/unlock" {
		d.serveUnlock(w, r, token, l, window, pick)
		return
	}
	if path == "/frame" {
		d.mu.Lock()
		tooSoon := time.Since(l.lastFrame) < 250*time.Millisecond
		if !tooSoon {
			l.lastFrame = time.Now()
		}
		d.mu.Unlock()
		if tooSoon {
			http.Error(w, "Too many requests", 429)
			return
		}
		d.keepAwake(l, false)
		if d.unlockAllowed(window, pick) && d.screenLocked() {
			w.Header().Set(desktopLockedHeader, "1")
			http.Error(w, "Mac is locked. Unlock it first.", http.StatusLocked)
			return
		}
		var data []byte
		var err error
		if window != nil {
			data, err = d.CaptureWindow(*window)
		} else if display != nil {
			data, err = d.CaptureDisplay(*display)
		} else {
			data, err = d.Capture()
		}
		// A sleeping display drops out of the shareable list. Wake it and
		// retry once after it reappears instead of reporting a stale state.
		if err != nil && d.Wake != nil && !errors.Is(err, desktop.ErrWindowUnavailable) && !errors.Is(err, desktop.ErrDisplayUnavailable) {
			d.keepAwake(l, true)
			time.Sleep(500 * time.Millisecond)
			if window != nil {
				data, err = d.CaptureWindow(*window)
			} else if display != nil {
				data, err = d.CaptureDisplay(*display)
			} else {
				data, err = d.Capture()
			}
		}
		if err != nil {
			if errors.Is(err, desktop.ErrDisplayUnavailable) {
				d.mu.Lock()
				l.capturedAt = time.Time{}
				d.mu.Unlock()
				http.Error(w, err.Error(), 409)
				return
			}
			if window != nil && errors.Is(err, desktop.ErrWindowUnavailable) {
				d.Revoke(token)
				http.Error(w, "The authorization window closed. Close this view to return to the conversation.", 410)
				return
			}
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		if len(data) == 0 || len(data) > 2<<20 {
			http.Error(w, "Invalid or oversized frame", 503)
			return
		}
		d.mu.Lock()
		valid := d.valid(token) == l
		if valid {
			l.capturedAt = time.Now()
		}
		d.mu.Unlock()
		if !valid || r.Context().Err() != nil {
			http.Error(w, "Remote control ended", 410)
			return
		}
		if d.unlockAllowed(window, pick) && d.screenLocked() {
			w.Header().Set(desktopLockedHeader, "1")
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(data)
		return
	}
	var request desktopInputRequest
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "Invalid operation", 400)
		return
	}
	in := request.Input
	if err := in.Validate(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.valid(token) != l || r.Context().Err() != nil {
		http.Error(w, "Remote control ended", 410)
		return
	}
	if (window == nil) != (l.window == nil) || window != nil && (window.ID != l.window.ID || window.PID != l.window.PID) {
		http.Error(w, "The target window changed. Wait for a new frame.", 409)
		return
	}
	if !sameDesktopDisplay(display, l.display) || !desktopInputMatchesDisplay(request, l) {
		http.Error(w, "The target display changed. Wait for a new frame.", 409)
		return
	}
	if time.Since(l.capturedAt) > 10*time.Second {
		http.Error(w, "Wait for a new desktop frame before continuing", 409)
		return
	}
	if time.Since(l.lastInput) < 20*time.Millisecond {
		http.Error(w, "Input is too frequent", 429)
		return
	}
	if l.actionPending {
		http.Error(w, "Checking the authorization result. Please wait.", 409)
		return
	}
	if l.unlocking || l.launching {
		http.Error(w, "The desktop is busy. Try again later.", http.StatusConflict)
		return
	}
	l.lastInput = time.Now()
	// Revoke and dispatch are serialized: no event can begin after revocation.
	var err error
	var action desktop.Action
	var acted bool
	var before []desktop.Window
	if window != nil {
		if d.Dialog != nil && l.onAction != nil && (in.Kind == "click" || in.Kind == "key") {
			if dialog, e := d.Dialog(*window); e == nil {
				action, acted = dialog.InputAction(in)
			}
		}
		if acted {
			before, err = d.actionWindows(*window)
			if err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
		}
		err = d.InputWindow(*window, in)
	} else if display != nil {
		err = d.InputDisplay(*display, in)
	} else {
		err = d.Input(in)
	}
	if err != nil {
		if errors.Is(err, desktop.ErrDisplayUnavailable) {
			l.capturedAt = time.Time{}
			http.Error(w, err.Error(), 409)
			return
		}
		http.Error(w, err.Error(), 403)
		return
	}
	if acted && l.onAction != nil {
		d.trackWindowActionLocked(l, action, before)
	}
	w.WriteHeader(http.StatusNoContent)
}

// Busy reports whether a phone holds a live lease.
func (d *Server) Busy() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lease != nil && d.valid(d.lease.token) != nil
}

// SetOnAction sends the window actions of token's lease to f, if the lease
// is live.
func (d *Server) SetOnAction(token string, f func(desktop.Window, desktop.Action, []desktop.Window)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if l := d.valid(token); l != nil {
		l.onAction = f
	}
}

// Holds reports whether token names a live lease.
func (d *Server) Holds(token string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.valid(token) != nil
}

// Capturing reports whether the current lease streams the screen.
func (d *Server) Capturing() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lease != nil && d.lease.active
}

// LeaseDone is closed when the current lease ends; nil without one.
func (d *Server) LeaseDone() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lease == nil {
		return nil
	}
	return d.lease.ctx.Done()
}

// SetLeaseWindow binds the current lease to window, or to the full desktop
// when nil, as the phone's menu does. Tests use it to set up a lease.
func (d *Server) SetLeaseWindow(window *desktop.Window) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lease != nil {
		d.lease.window = window
	}
}

// HoldWork takes the lock desktop actions run under until the returned func
// is called. Tests use it to keep the desktop busy.
func (d *Server) HoldWork() (release func()) {
	d.work.Lock()
	return d.work.Unlock
}
