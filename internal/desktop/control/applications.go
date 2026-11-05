package control

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// Launching apps belongs to menu leases. An authorization window cannot use
// this endpoint to change focus or start another application.
func (d *Server) serveApplications(w http.ResponseWriter, r *http.Request, token string, l *desktopLease, launch bool) {
	if (!launch && r.Method != http.MethodGet) || (launch && r.Method != http.MethodPost) {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	d.mu.Lock()
	allowed := d.valid(token) == l && l.pick
	d.mu.Unlock()
	if !allowed {
		http.Error(w, "App launching is not available in this view", http.StatusForbidden)
		return
	}
	if d.Apps == nil || d.OpenApp == nil {
		http.Error(w, "Update Miya on your Mac to open apps", http.StatusNotImplemented)
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if launch {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || body.ID == "" || decoder.Decode(new(any)) != io.EOF {
			http.Error(w, "Invalid app", http.StatusBadRequest)
			return
		}
	}
	// Keep app listing independent from slow ScreenCaptureKit frames/previews.
	apps, err := d.Apps()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	d.mu.Lock()
	alive := d.valid(token) == l && r.Context().Err() == nil
	d.mu.Unlock()
	if !alive {
		http.Error(w, "Remote control ended", http.StatusGone)
		return
	}
	if !launch {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apps)
		return
	}
	for _, app := range apps {
		if app.ID != body.ID {
			continue
		}
		if !d.work.TryLock() {
			http.Error(w, "The desktop is busy. Try again later.", http.StatusTooManyRequests)
			return
		}
		defer d.work.Unlock()
		if err := d.Authorize(); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		d.mu.Lock()
		if d.valid(token) != l || r.Context().Err() != nil {
			d.mu.Unlock()
			http.Error(w, "Remote control ended", http.StatusGone)
			return
		}
		if l.unlocking || l.launching {
			d.mu.Unlock()
			http.Error(w, "The desktop is busy. Try again later.", http.StatusConflict)
			return
		}
		l.launching = true
		l.capturedAt = time.Time{}
		d.mu.Unlock()
		defer func() { d.mu.Lock(); l.launching = false; d.mu.Unlock() }()
		if d.Locked != nil {
			locked, err := d.Locked()
			if err != nil {
				http.Error(w, "Unable to read Mac lock status", http.StatusServiceUnavailable)
				return
			}
			if locked {
				http.Error(w, "Mac is locked. Unlock it first.", http.StatusLocked)
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		stop := context.AfterFunc(l.ctx, cancel)
		defer cancel()
		defer stop()
		if err := ctx.Err(); err != nil || l.ctx.Err() != nil {
			http.Error(w, "Remote control ended", http.StatusGone)
			return
		}
		if err := d.OpenApp(ctx, app.ID); err != nil {
			if ctx.Err() != nil || l.ctx.Err() != nil {
				http.Error(w, "Remote control ended", http.StatusGone)
				return
			}
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		d.mu.Lock()
		alive = d.valid(token) == l && ctx.Err() == nil
		d.mu.Unlock()
		if !alive {
			http.Error(w, "Remote control ended", http.StatusGone)
			return
		}
		app.Icon = nil
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(app)
		return
	}
	http.Error(w, "This app is no longer available. Refresh the app list.", http.StatusNotFound)
}
