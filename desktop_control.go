package main

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

const desktopControlPrefix = "/.miya-desktop/v1"
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
	pick bool
}

// One operator at a time. A lease belongs to one live phone control
// connection and expires even if a client keeps polling forever.
type desktopControl struct {
	mu            sync.Mutex
	work          sync.Mutex
	lease         *desktopLease
	authorize     func() error
	capture       func() ([]byte, error)
	input         func(desktop.Input) error
	windows       func() ([]desktop.Window, error)
	captureWindow func(desktop.Window) ([]byte, error)
	inputWindow   func(desktop.Window, desktop.Input) error
	dialog        func(desktop.Window) (desktop.Dialog, error)
	pressAction   func(desktop.Window, string) error
	wake          func() error
}

func (c *chatController) desktopControls() *desktopControl {
	c.desktopMu.Lock()
	defer c.desktopMu.Unlock()
	if c.desktop == nil {
		c.desktop = &desktopControl{authorize: desktop.Authorize, capture: desktop.Capture, input: desktop.SendInput, windows: desktop.Windows, captureWindow: desktop.CaptureWindow, inputWindow: desktop.SendWindowInput, dialog: desktop.WindowDialog, pressAction: desktop.PressWindowAction, wake: desktop.WakeDisplay}
	}
	return c.desktop
}

func (c *chatController) desktopBusy() bool {
	c.desktopMu.Lock()
	d := c.desktop
	c.desktopMu.Unlock()
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lease != nil && d.valid(d.lease.token) != nil
}

func (d *desktopControl) open(ctx context.Context) (string, error) {
	return d.openWindow(ctx, nil)
}

func (d *desktopControl) openWindow(ctx context.Context, window *desktop.Window) (string, error) {
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
		return "", errors.New("另一个远程桌面界面尚未关闭，请先结束控制")
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
	l.stop = context.AfterFunc(leaseCtx, func() { d.revoke(l.token) })
	// Release the deadline timer as well as the session when manually closed.
	stop := l.stop
	l.stop = func() bool { result := stop(); cancel(); return result }
	d.lease = l
	return l.token, nil
}

// keepAwake lights a sleeping display before capture and keeps it lit while
// an operator polls. It only declares user activity; once polling stops the
// Mac's own idle sleep resumes. A failure surfaces as the capture error.
func (d *desktopControl) keepAwake(l *desktopLease, force bool) {
	if d.wake == nil {
		return
	}
	d.mu.Lock()
	due := force || time.Since(l.lastWake) >= 5*time.Second
	if due {
		l.lastWake = time.Now()
	}
	d.mu.Unlock()
	if due {
		_ = d.wake()
	}
}

// allowPick lets a lease opened from the phone menu choose any shareable
// window or the full desktop. Authorization leases never get it.
func (d *desktopControl) allowPick(token string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if l := d.valid(token); l != nil {
		l.pick = true
	}
}

func (d *desktopControl) revoke(token string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lease != nil && d.lease.token == token {
		l := d.lease
		d.lease = nil
		l.stop()
	}
}

func (d *desktopControl) valid(token string) *desktopLease {
	l := d.lease
	if token == "" || l == nil || l.token != token || l.ctx.Err() != nil || !time.Now().Before(l.expires) {
		return nil
	}
	return l
}

func (d *desktopControl) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	d.mu.Lock()
	l := d.valid(token)
	d.mu.Unlock()
	if l == nil {
		http.Error(w, "远程控制已结束或连接已断开，请返回对话重新打开", http.StatusGone)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, desktopControlPrefix)
	if path == "/stop" && r.Method == http.MethodPost {
		d.revoke(token)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if path == "/session" && r.Method == http.MethodGet {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.valid(token) != l {
			http.Error(w, "远程控制已结束", 410)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			WindowOnly bool            `json:"windowOnly"`
			Window     *desktop.Window `json:"window"`
			Pick       bool            `json:"pick"`
		}{l.window != nil, l.window, l.pick})
		return
	}
	if path == "/actions" || path == "/action" {
		d.serveWindowActions(w, r, token, l, path == "/action")
		return
	}
	if path != "/frame" && path != "/start" && path != "/input" && path != "/windows" && path != "/select" && path != "/thumbnail" {
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
	if path != "/input" {
		if !d.work.TryLock() {
			http.Error(w, "桌面正忙，请稍后重试", http.StatusTooManyRequests)
			return
		}
		defer d.work.Unlock()
	}
	d.mu.Lock()
	if d.valid(token) != l {
		d.mu.Unlock()
		http.Error(w, "远程控制已结束", 410)
		return
	}
	active := l.active
	pick := l.pick
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
			http.Error(w, "此界面未启用单窗口操作", 400)
			return
		}
		windows, err := d.windows()
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
			http.Error(w, "无效的窗口", 400)
			return
		}
		// Window 0 is the full desktop, offered only to menu leases.
		if pick && selected.ID == 0 && selected.PID == 0 {
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.valid(token) != l {
				http.Error(w, "远程控制已结束", 410)
				return
			}
			l.window = nil
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
				http.Error(w, "远程控制已结束", 410)
				return
			}
			if l.actionPending {
				http.Error(w, "正在确认授权结果，请稍候", 409)
				return
			}
			l.window = &candidate
			l.active = false
			l.capturedAt = time.Time{}
			l.lastFrame = time.Time{}
			w.WriteHeader(204)
			return
		}
		http.Error(w, "窗口已关闭，请刷新列表", 404)
		return
	}
	if path == "/start" {
		if err := d.authorize(); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		d.keepAwake(l, true)
		if window != nil && window.ID == 0 {
			http.Error(w, "请先选择目标窗口", 409)
			return
		}
		d.mu.Lock()
		if d.valid(token) != l {
			d.mu.Unlock()
			http.Error(w, "远程控制已结束", 410)
			return
		}
		l.active = true
		d.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !active {
		http.Error(w, "请先确认开始远程控制", http.StatusForbidden)
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
			http.Error(w, "请求过快", 429)
			return
		}
		d.keepAwake(l, false)
		var data []byte
		var err error
		if window != nil {
			data, err = d.captureWindow(*window)
		} else {
			data, err = d.capture()
		}
		// A sleeping display drops out of the shareable list. Wake it and
		// retry once after it reappears instead of reporting a stale state.
		if err != nil && d.wake != nil && !errors.Is(err, desktop.ErrWindowUnavailable) {
			d.keepAwake(l, true)
			time.Sleep(500 * time.Millisecond)
			if window != nil {
				data, err = d.captureWindow(*window)
			} else {
				data, err = d.capture()
			}
		}
		if err != nil {
			if window != nil && errors.Is(err, desktop.ErrWindowUnavailable) {
				d.revoke(token)
				http.Error(w, "目标授权窗口已关闭。关闭此界面返回会话。", 410)
				return
			}
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		if len(data) == 0 || len(data) > 2<<20 {
			http.Error(w, "画面无效或过大", 503)
			return
		}
		d.mu.Lock()
		valid := d.valid(token) == l
		if valid {
			l.capturedAt = time.Now()
		}
		d.mu.Unlock()
		if !valid || r.Context().Err() != nil {
			http.Error(w, "远程控制已结束", 410)
			return
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(data)
		return
	}
	var in desktop.Input
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		http.Error(w, "无效的操作", 400)
		return
	}
	if err := in.Validate(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.valid(token) != l || r.Context().Err() != nil {
		http.Error(w, "远程控制已结束", 410)
		return
	}
	if (window == nil) != (l.window == nil) || window != nil && (window.ID != l.window.ID || window.PID != l.window.PID) {
		http.Error(w, "目标窗口已切换，请等待新画面", 409)
		return
	}
	if time.Since(l.capturedAt) > 10*time.Second {
		http.Error(w, "请等待新的桌面画面后再操作", 409)
		return
	}
	if time.Since(l.lastInput) < 20*time.Millisecond {
		http.Error(w, "操作过快", 429)
		return
	}
	if l.actionPending {
		http.Error(w, "正在确认授权结果，请稍候", 409)
		return
	}
	l.lastInput = time.Now()
	// Revoke and dispatch are serialized: no event can begin after revocation.
	var err error
	var action desktop.Action
	var acted bool
	var before []desktop.Window
	if window != nil {
		if d.dialog != nil && l.onAction != nil && (in.Kind == "click" || in.Kind == "key") {
			if dialog, e := d.dialog(*window); e == nil {
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
		err = d.inputWindow(*window, in)
	} else {
		err = d.input(in)
	}
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	if acted && l.onAction != nil {
		d.trackWindowActionLocked(l, action, before)
	}
	w.WriteHeader(http.StatusNoContent)
}
