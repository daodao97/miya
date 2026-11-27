package control

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"miya/internal/desktop"
)

const (
	MinFrameEdge             = 320
	MaxFrameEdge             = 4096
	FrameInterval            = 250 * time.Millisecond
	InteractiveFrameInterval = 90 * time.Millisecond
	InteractiveFrameMaxEdge  = 1280
)

func frameOptions(query url.Values) (desktop.FrameOptions, bool) {
	if !query.Has("maxEdge") && !query.Has("quality") {
		return desktop.FrameOptions{}, true
	}
	edge, edgeErr := strconv.Atoi(query.Get("maxEdge"))
	quality, qualityErr := strconv.Atoi(query.Get("quality"))
	if edgeErr != nil || qualityErr != nil || edge < MinFrameEdge || edge > MaxFrameEdge || quality < 50 || quality > 90 {
		return desktop.FrameOptions{}, false
	}
	return desktop.FrameOptions{MaxEdge: edge, Quality: quality}, true
}

// captureFrame is called while the transport holds the capture lock.
func (d *Server) captureFrame(ctx context.Context, l *desktopLease, display *desktop.Display, window *desktop.Window, options desktop.FrameOptions, pick bool) ([]byte, bool, *HTTPError) {
	d.keepAwake(l, false)
	if d.unlockAllowed(window, pick) && d.screenLocked() {
		d.mu.Lock()
		l.capturedAt = time.Time{}
		d.mu.Unlock()
		return nil, true, &HTTPError{Status: http.StatusLocked, Message: "Mac is locked. Unlock it first."}
	}
	capture := func() ([]byte, error) {
		switch {
		case options.MaxEdge != 0 && d.CaptureFrame != nil:
			return d.CaptureFrame(ctx, display, window, options)
		case window != nil:
			return d.CaptureWindow(*window)
		case display != nil:
			return d.CaptureDisplay(*display)
		default:
			return d.Capture()
		}
	}
	data, err := capture()
	// A sleeping display drops out of the shareable list. Wake it and
	// retry once after it reappears instead of reporting a stale state.
	if err != nil && d.Wake != nil && ctx.Err() == nil && !errors.Is(err, desktop.ErrWindowUnavailable) && !errors.Is(err, desktop.ErrDisplayUnavailable) {
		d.keepAwake(l, true)
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, false, &HTTPError{Status: http.StatusGone, Message: "Remote control ended"}
		case <-timer.C:
		}
		data, err = capture()
	}
	if err != nil {
		if errors.Is(err, desktop.ErrDisplayUnavailable) {
			d.mu.Lock()
			l.capturedAt = time.Time{}
			d.mu.Unlock()
			return nil, false, &HTTPError{Status: 409, Message: err.Error()}
		}
		if window != nil && errors.Is(err, desktop.ErrWindowUnavailable) {
			d.Revoke(l.token)
			return nil, false, &HTTPError{Status: 410, Message: "The authorization window closed. Close this view to return to the conversation."}
		}
		return nil, false, &HTTPError{Status: http.StatusServiceUnavailable, Message: err.Error()}
	}
	if len(data) == 0 || len(data) > 2<<20 {
		return nil, false, &HTTPError{Status: 503, Message: "Invalid or oversized frame"}
	}
	d.mu.Lock()
	valid := d.valid(l.token) == l && ctx.Err() == nil
	if valid {
		l.capturedAt = time.Now()
	}
	d.mu.Unlock()
	if !valid {
		return nil, false, &HTTPError{Status: 410, Message: "Remote control ended"}
	}
	return data, d.unlockAllowed(window, pick) && d.screenLocked(), nil
}
