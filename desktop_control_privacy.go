package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

type desktopPrivacyState struct {
	Enabled bool `json:"enabled"`
}

func (d *desktopControl) privacyState(l *desktopLease) desktopPrivacyState {
	state := desktopPrivacyState{Enabled: l.privacy != nil}
	if l.privacy != nil {
		select {
		case <-l.privacy.Done():
			l.privacy = nil
			state.Enabled = false
		default:
		}
	}
	return state
}

func (d *desktopControl) servePrivacy(w http.ResponseWriter, r *http.Request, token string, l *desktopLease) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	d.mu.Lock()
	allowed := d.valid(token) == l && l.pick
	d.mu.Unlock()
	if !allowed {
		http.Error(w, "Privacy mode is not available in this view", 403)
		return
	}
	if d.privacy == nil {
		http.Error(w, "Update Miya on your Mac to use privacy mode", 501)
		return
	}
	if r.Method == http.MethodGet {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.valid(token) != l {
			http.Error(w, "Remote control ended", 410)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(d.privacyState(l))
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || body.Enabled == nil || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "Invalid privacy mode request", 400)
		return
	}
	if !d.privacyWork.TryLock() {
		http.Error(w, "Privacy mode is changing. Try again shortly.", 429)
		return
	}
	defer d.privacyWork.Unlock()
	d.mu.Lock()
	if d.valid(token) != l || r.Context().Err() != nil {
		d.mu.Unlock()
		http.Error(w, "Remote control ended", 410)
		return
	}
	current := d.privacyState(l)
	if current.Enabled == *body.Enabled {
		d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(current)
		return
	}
	if !*body.Enabled {
		privacy := l.privacy
		l.privacy = nil
		d.mu.Unlock()
		_ = privacy.Close()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(desktopPrivacyState{})
		return
	}
	if !l.active || l.capturedAt.IsZero() || time.Since(l.capturedAt) > 10*time.Second {
		d.mu.Unlock()
		http.Error(w, "Wait for a new desktop frame before continuing", 409)
		return
	}
	if l.unlocking || l.launching {
		d.mu.Unlock()
		http.Error(w, "The desktop is busy. Try again later.", 409)
		return
	}
	d.mu.Unlock()
	if d.locked != nil {
		locked, err := d.locked()
		if err != nil {
			http.Error(w, "Unable to read Mac lock status", 503)
			return
		}
		if locked {
			http.Error(w, "Unlock your Mac before enabling privacy mode", 423)
			return
		}
	}
	ctx, cancel := context.WithCancel(l.ctx)
	// Request cancellation cancels startup only. Once enabled, the lease owns
	// the helper and its cancellation; an HTTP response ending must not unblank.
	stopRequest := context.AfterFunc(r.Context(), cancel)
	privacy, err := d.privacy(ctx)
	stopRequest()
	if err != nil {
		cancel()
		if l.ctx.Err() != nil || r.Context().Err() != nil {
			http.Error(w, "Remote control ended", 410)
			return
		}
		http.Error(w, err.Error(), 503)
		return
	}
	d.mu.Lock()
	if d.valid(token) != l || r.Context().Err() != nil || ctx.Err() != nil {
		d.mu.Unlock()
		cancel()
		_ = privacy.Close()
		http.Error(w, "Remote control ended", 410)
		return
	}
	select {
	case <-privacy.Done():
		d.mu.Unlock()
		cancel()
		http.Error(w, "Privacy mode stopped before it could be enabled", 503)
		return
	default:
	}
	l.privacy = privacy
	d.mu.Unlock()
	go func() { <-privacy.Done(); cancel() }()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(desktopPrivacyState{Enabled: true})
}
