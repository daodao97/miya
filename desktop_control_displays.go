package main

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"miya/internal/desktop"
)

type desktopInputRequest struct {
	desktop.Input
	DisplayID   uint32 `json:"displayId,omitempty"`
	DisplayUUID string `json:"displayUUID,omitempty"`
}

func sameDesktopDisplay(a, b *desktop.Display) bool {
	return a == nil && b == nil || a != nil && b != nil && a.ID == b.ID && a.UUID == b.UUID
}

func desktopInputMatchesDisplay(in desktopInputRequest, l *desktopLease) bool {
	if in.DisplayID == 0 && in.DisplayUUID == "" {
		return !l.displayInputRequired
	}
	return l.window == nil && l.display != nil && in.DisplayID == l.display.ID && in.DisplayUUID == l.display.UUID
}

func (d *desktopControl) displaysAvailable() bool {
	return d.displays != nil && d.captureDisplay != nil && d.inputDisplay != nil
}

func (d *desktopControl) resolveDisplay(want *desktop.Display) (desktop.Display, error) {
	displays, err := d.displays()
	if err != nil {
		return desktop.Display{}, err
	}
	for _, display := range displays {
		if want != nil && display.ID == want.ID && display.UUID == want.UUID || want == nil && display.Main {
			return display, nil
		}
	}
	if want == nil && len(displays) > 0 {
		return displays[0], nil
	}
	return desktop.Display{}, desktop.ErrDisplayUnavailable
}

func (d *desktopControl) serveDisplays(w http.ResponseWriter, r *http.Request, token string, l *desktopLease, selectDisplay bool) {
	if selectDisplay && r.Method != http.MethodPost || !selectDisplay && r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", 405)
		return
	}
	d.mu.Lock()
	allowed := d.valid(token) == l && l.pick
	d.mu.Unlock()
	if !allowed {
		http.Error(w, "Display selection is not available in this view", 403)
		return
	}
	if !d.displaysAvailable() {
		http.Error(w, "Update Miya on your Mac to select displays", 501)
		return
	}
	var selected desktop.Display
	if selectDisplay {
		var body struct {
			ID   uint32 `json:"id"`
			UUID string `json:"uuid"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || body.ID == 0 || body.UUID == "" || len(body.UUID) > 128 || decoder.Decode(new(any)) != io.EOF {
			http.Error(w, "Invalid display", 400)
			return
		}
		selected.ID, selected.UUID = body.ID, body.UUID
		if !d.work.TryLock() {
			http.Error(w, "The desktop is busy. Try again later.", 429)
			return
		}
		defer d.work.Unlock()
	}
	d.keepAwake(l, false)
	displays, err := d.displays()
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.valid(token) != l || r.Context().Err() != nil {
		http.Error(w, "Remote control ended", 410)
		return
	}
	if !selectDisplay {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(displays)
		return
	}
	if l.unlocking || l.launching || l.actionPending {
		http.Error(w, "The desktop is busy. Try again later.", 409)
		return
	}
	for _, display := range displays {
		if display.ID != selected.ID || display.UUID != selected.UUID {
			continue
		}
		copy := display
		l.display, l.window, l.displayInputRequired = &copy, nil, true
		l.active, l.capturedAt, l.lastFrame = false, time.Time{}, time.Time{}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(display)
		return
	}
	http.Error(w, desktop.ErrDisplayUnavailable.Error(), 409)
}
