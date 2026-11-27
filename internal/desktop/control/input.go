package control

import (
	"context"
	"errors"
	"net/http"
	"time"

	"miya/internal/desktop"
)

// performInput binds an input to the target captured by its transport.
func (d *Server) performInput(ctx context.Context, l *desktopLease, display *desktop.Display, window *desktop.Window, request desktopInputRequest) *HTTPError {
	in := request.Input
	if err := in.Validate(); err != nil {
		return &HTTPError{Status: 400, Message: err.Error()}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.valid(l.token) != l || ctx.Err() != nil {
		return &HTTPError{Status: 410, Message: "Remote control ended"}
	}
	if !l.active {
		return &HTTPError{Status: 409, Message: "Wait for a new desktop frame before continuing"}
	}
	if (window == nil) != (l.window == nil) || window != nil && (window.ID != l.window.ID || window.PID != l.window.PID) {
		return &HTTPError{Status: 409, Message: "The target window changed. Wait for a new frame."}
	}
	if !sameDesktopDisplay(display, l.display) || !desktopInputMatchesDisplay(request, l) {
		return &HTTPError{Status: 409, Message: "The target display changed. Wait for a new frame."}
	}
	if time.Since(l.capturedAt) > 10*time.Second {
		return &HTTPError{Status: 409, Message: "Wait for a new desktop frame before continuing"}
	}
	if time.Since(l.lastInput) < 20*time.Millisecond {
		return &HTTPError{Status: 429, Message: "Input is too frequent"}
	}
	if l.actionPending {
		return &HTTPError{Status: 409, Message: "Checking the authorization result. Please wait."}
	}
	if l.unlocking || l.launching {
		return &HTTPError{Status: http.StatusConflict, Message: "The desktop is busy. Try again later."}
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
				return &HTTPError{Status: 409, Message: err.Error()}
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
			return &HTTPError{Status: 409, Message: err.Error()}
		}
		return &HTTPError{Status: 403, Message: err.Error()}
	}
	if acted && l.onAction != nil {
		d.trackWindowActionLocked(l, action, before)
	}
	return nil
}
