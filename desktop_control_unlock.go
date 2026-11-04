package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"miya/internal/desktop"
)

const desktopLockedHeader = "X-Miya-Screen-Locked"
const desktopUnlockMaxBytes = 256

// Menu leases may unlock; authorization leases stay bound to their window.
func (d *desktopControl) unlockAllowed(window *desktop.Window, pick bool) bool {
	return d.locked != nil && (window == nil || pick)
}

func (d *desktopControl) screenLocked() bool {
	if d.locked == nil {
		return false
	}
	locked, err := d.locked()
	return err == nil && locked
}

func desktopWait(ctx context.Context, duration time.Duration) bool {
	t := time.NewTimer(duration)
	defer t.Stop()
	select {
	case <-t.C:
		return ctx.Err() == nil
	case <-ctx.Done():
		return false
	}
}

func validDesktopUnlockPassword(password string) bool {
	return password != "" && len(password) <= desktopUnlockMaxBytes && utf8.ValidString(password) && !strings.ContainsAny(password, "\x00\r\n")
}

// serveUnlock never logs or echoes credentials or native input errors. All
// input is serialized with revocation and checked against the current lock
// state, including after waking the screen. An unlock never retries a password.
func (d *desktopControl) serveUnlock(w http.ResponseWriter, r *http.Request, token string, l *desktopLease, window *desktop.Window, pick bool) {
	if !d.unlockAllowed(window, pick) {
		http.Error(w, "This view cannot unlock the Mac", http.StatusForbidden)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || !validDesktopUnlockPassword(body.Password) || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "Invalid unlock password", http.StatusBadRequest)
		return
	}
	defer func() { body.Password = "" }()
	d.mu.Lock()
	if d.valid(token) != l || !l.active {
		d.mu.Unlock()
		http.Error(w, "Remote control ended", http.StatusGone)
		return
	}
	if l.unlocking || l.launching {
		d.mu.Unlock()
		http.Error(w, "The desktop is busy. Try again later.", http.StatusTooManyRequests)
		return
	}
	l.unlocking = true
	l.capturedAt = time.Time{}
	d.mu.Unlock()
	defer func() { d.mu.Lock(); l.unlocking = false; d.mu.Unlock() }()
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(l.ctx, cancel)
	defer cancel()
	defer stop()
	// Check before wake, and again immediately before each dispatched event.
	checkLocked := func() bool {
		locked, err := d.locked()
		if err != nil {
			http.Error(w, "Unable to read Mac lock status", http.StatusServiceUnavailable)
			return false
		}
		if !locked {
			http.Error(w, "Mac is not locked", http.StatusConflict)
			return false
		}
		return true
	}
	if !checkLocked() {
		return
	}
	d.keepAwake(l, true)
	if !desktopWait(ctx, 700*time.Millisecond) {
		http.Error(w, "Remote control ended", http.StatusGone)
		return
	}
	steps := []desktop.Input{{Kind: "key", Key: "a", Modifier: "command"}, {Kind: "text", Text: body.Password}, {Kind: "key", Key: "enter"}}
	body.Password = ""
	defer func() { clear(steps) }()
	for i := range steps {
		if i > 0 && !desktopWait(ctx, 150*time.Millisecond) {
			http.Error(w, "Remote control ended", http.StatusGone)
			return
		}
		d.mu.Lock()
		if d.valid(token) != l || ctx.Err() != nil {
			d.mu.Unlock()
			http.Error(w, "Remote control ended", http.StatusGone)
			return
		}
		if !checkLocked() {
			d.mu.Unlock()
			return
		}
		l.lastInput = time.Now()
		err := d.input(steps[i])
		steps[i].Text = ""
		d.mu.Unlock()
		if err != nil {
			http.Error(w, "Unable to enter text on the Mac lock screen. Check Accessibility permission.", http.StatusForbidden)
			return
		}
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if !desktopWait(ctx, 250*time.Millisecond) {
			http.Error(w, "Remote control ended", http.StatusGone)
			return
		}
		locked, err := d.locked()
		if err != nil {
			http.Error(w, "Unable to read Mac lock status", http.StatusServiceUnavailable)
			return
		}
		d.mu.Lock()
		alive := d.valid(token) == l && ctx.Err() == nil
		d.mu.Unlock()
		if !alive {
			http.Error(w, "Remote control ended", http.StatusGone)
			return
		}
		if !locked {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	http.Error(w, "Mac is still locked. The password may be incorrect.", http.StatusUnauthorized)
}
