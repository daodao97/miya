package control

import (
	"encoding/json"
	"net/http"
	"time"

	"miya/internal/desktop"
)

// These controls belong to the selected approval window, not the foreground
// desktop. No editable values are included in the returned dialog metadata.
func (d *Server) serveWindowActions(w http.ResponseWriter, r *http.Request, token string, lease *desktopLease, press bool) {
	method := http.MethodGet
	if press {
		method = http.MethodPost
	}
	if r.Method != method {
		http.Error(w, "Method not allowed", 405)
		return
	}
	var request struct {
		ID        string `json:"id"`
		WindowID  uint32 `json:"windowId"`
		WindowPID int    `json:"windowPid"`
	}
	if press {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil || request.ID == "" {
			http.Error(w, "Invalid authorization action", 400)
			return
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.valid(token) != lease || r.Context().Err() != nil {
		http.Error(w, "Remote control ended", 410)
		return
	}
	if !lease.active || lease.window == nil || lease.onAction == nil || d.Dialog == nil {
		http.Error(w, "No authorization actions are available in this view", 403)
		return
	}
	if press && (request.WindowID != lease.window.ID || request.WindowPID != lease.window.PID || d.PressAction == nil) {
		http.Error(w, "The authorization window changed. Refresh the action buttons.", 409)
		return
	}
	if !press {
		dialog, err := d.Dialog(*lease.window)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		if d.valid(token) != lease || r.Context().Err() != nil {
			http.Error(w, "Remote control ended", 410)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Window  desktop.Window   `json:"window"`
			Actions []desktop.Action `json:"actions"`
			Pending bool             `json:"pending"`
		}{*lease.window, dialog.Actions, lease.actionPending})
		return
	}
	if lease.actionPending {
		http.Error(w, "Checking the authorization result. Please wait.", 409)
		return
	}
	if time.Since(lease.capturedAt) > 10*time.Second {
		http.Error(w, "Wait for a new window frame before continuing", 409)
		return
	}
	if time.Since(lease.lastInput) < 20*time.Millisecond {
		http.Error(w, "Input is too frequent", 429)
		return
	}
	before, err := d.actionWindows(*lease.window)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	action, err := d.pressNativeWindowAction(*lease.window, request.ID)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	lease.lastInput = time.Now()
	d.trackWindowActionLocked(lease, action, before)
	w.WriteHeader(http.StatusNoContent)
}

func (d *Server) trackWindowActionLocked(lease *desktopLease, action desktop.Action, before []desktop.Window) {
	if lease.onAction == nil {
		return
	}
	lease.actionPending = true
	window := *lease.window
	go func() {
		lease.onAction(window, action, before)
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.valid(lease.token) == lease {
			lease.actionPending = false
		}
	}()
}
