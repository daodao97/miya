package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"miya/internal/desktop"
	"miya/internal/remote"
)

type phoneDesktopPreview struct {
	RequestID string          `json:"requestId"`
	Approval  json.RawMessage `json:"approval"`
	Window    desktop.Window  `json:"window"`
	Dialog    desktop.Dialog  `json:"dialog"`
	Data      []byte          `json:"data,omitempty"`
}

type desktopWindowHint struct {
	App    string          `json:"app,omitempty"`
	Title  string          `json:"title,omitempty"`
	Window *desktop.Window `json:"window,omitempty"`
}

func (c *chatController) phoneDesktopHint(r remote.Request) desktopWindowHint {
	if r.WindowID != 0 && r.WindowPID > 0 {
		return desktopWindowHint{Window: &desktop.Window{ID: r.WindowID, PID: r.WindowPID}}
	}
	for _, a := range c.approvals() {
		if a.Method == "miya/desktopControl" && sameJSON(a.ID, r.Approval) {
			var hint desktopWindowHint
			_ = json.Unmarshal(a.Payload, &hint)
			return hint
		}
	}
	return desktopWindowHint{}
}

func (c *chatController) completeDesktopAction(r remote.Request, action desktop.Action) {
	if !c.phoneDesktopApproval(r) {
		return
	}
	for _, a := range c.approvals() {
		if sameJSON(a.ID, r.Approval) && a.Method == "miya/desktopControl" {
			_ = c.phoneApprovalResponse(remote.Request{Approval: a.ID, ApprovalVersion: r.ApprovalVersion, Allow: !action.Cancel})
			return
		}
	}
}

// Button titles are display data, never outcome classifiers. An observed native
// action completes a takeover only after its exact window stays gone, with no
// successor window from the same owner appearing during the settling interval.
func (c *chatController) confirmDesktopAction(ctx context.Context, r remote.Request, action desktop.Action, before []desktop.Window) {
	if r.WindowID == 0 || r.WindowPID <= 0 || len(before) == 0 {
		return
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	var absentSince time.Time
	for {
		windows, err := c.desktopControls().windows()
		if err != nil {
			// A failed read is not evidence that the dialog closed.
			absentSince = time.Time{}
		} else {
			present, successor := desktopActionWindows(r.WindowID, r.WindowPID, before, windows)
			if successor {
				return // Another step needs the user's input; retain the approval.
			}
			if present {
				absentSince = time.Time{}
			} else if absentSince.IsZero() {
				absentSince = time.Now()
			} else if time.Since(absentSince) >= 300*time.Millisecond {
				_, _ = onMain(ctx, c.dispatch, func() bool { c.completeDesktopAction(r, action); return true })
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-tick.C:
		}
	}
}

func desktopActionWindows(id uint32, pid int, before, after []desktop.Window) (present, successor bool) {
	var target desktop.Window
	known := make(map[[2]int]bool, len(before))
	for _, w := range before {
		known[[2]int{int(w.ID), w.PID}] = true
		if w.ID == id && w.PID == pid {
			target = w
		}
	}
	for _, w := range after {
		if w.ID == id && w.PID == pid {
			present = true
		}
		if !known[[2]int{int(w.ID), w.PID}] && (w.PID == pid || target.BundleID != "" && w.BundleID == target.BundleID) {
			successor = true
		}
	}
	return
}

// Capture the owner/window set before dispatch, so a transition to a new dialog
// cannot be mistaken for completion. Failure leaves the original action unsent.
func (d *desktopControl) actionWindows(window desktop.Window) ([]desktop.Window, error) {
	if d.windows == nil {
		return nil, errors.New("Unable to determine the target window state. Try again.")
	}
	windows, err := d.windows()
	if err != nil {
		return nil, err
	}
	for _, w := range windows {
		if w.ID == window.ID && w.PID == window.PID {
			return windows, nil
		}
	}
	return nil, desktop.ErrWindowUnavailable
}

func (d *desktopControl) performWindowAction(window desktop.Window, id string) (desktop.Action, []desktop.Window, error) {
	if !d.work.TryLock() {
		return desktop.Action{}, nil, errors.New("The permission window is busy. Try again later.")
	}
	defer d.work.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lease != nil && d.valid(d.lease.token) != nil {
		return desktop.Action{}, nil, errors.New("Close the window controls first")
	}
	before, err := d.actionWindows(window)
	if err != nil {
		return desktop.Action{}, nil, err
	}
	action, err := d.pressNativeWindowAction(window, id)
	return action, before, err
}

// The caller serializes dispatch with revocation and reads current native state.
func (d *desktopControl) pressNativeWindowAction(window desktop.Window, id string) (desktop.Action, error) {
	dialog, err := d.dialog(window)
	if err != nil {
		return desktop.Action{}, err
	}
	var match desktop.Action
	count := 0
	for _, a := range dialog.Actions {
		if a.ID == id {
			match = a
			count++
		}
	}
	if count != 1 || !match.Enabled {
		return desktop.Action{}, errors.New("The permission button is unavailable. Enter your password or refresh the preview.")
	}
	if err := d.pressAction(window, match.ID); err != nil {
		return desktop.Action{}, err
	}
	return match, nil
}

func (c *chatController) sendPhoneDesktopAction(ctx context.Context, r remote.Request, allowed bool, send func(remote.Frame) bool) {
	p := phoneDesktopPreview{RequestID: r.RequestID, Approval: r.Approval}
	var err error
	if allowed {
		var alive bool
		allowed, alive = onMain(ctx, c.dispatch, func() bool { return c.phoneDesktopApproval(r) })
		if !alive {
			return
		}
	}
	if !allowed {
		err = errors.New("This permission request was already handled or belongs to another session")
	} else {
		var action desktop.Action
		var before []desktop.Window
		action, before, err = c.desktopControls().performWindowAction(desktop.Window{ID: r.WindowID, PID: r.WindowPID}, r.DesktopAction)
		if err == nil {
			c.confirmDesktopAction(ctx, r, action, before)
		}
	}
	data, _ := json.Marshal(p)
	f := remote.Frame{Type: "desktop-action", Data: data}
	if err != nil {
		f.Error = err.Error()
	}
	send(f)
}

func chooseDesktopWindow(windows []desktop.Window, hint desktopWindowHint) (desktop.Window, error) {
	var matches []desktop.Window
	for _, w := range windows {
		if hint.Window != nil && (w.ID != hint.Window.ID || w.PID != hint.Window.PID) {
			continue
		}
		if hint.App != "" && !strings.EqualFold(w.App, hint.App) && !strings.EqualFold(w.BundleID, hint.App) {
			continue
		}
		if hint.Title != "" && !strings.Contains(strings.ToLower(w.Title), strings.ToLower(hint.Title)) {
			continue
		}
		matches = append(matches, w)
	}
	if len(matches) == 0 {
		return desktop.Window{}, errors.New("Permission window unavailable. Select a window or open it on your Mac.")
	}
	if len(matches) > 1 && (hint.App != "" || hint.Title != "" || hint.Window != nil) {
		return desktop.Window{}, errors.New("Multiple matching windows found. Select the permission window.")
	}
	return matches[0], nil
}

// Called on the controller executor. A preview is authorized only while the
// exact approval in the requested conversation is still pending.
func (c *chatController) phoneDesktopApproval(r remote.Request) bool {
	if r.ServerID == "" || !c.phoneThreadFor(r).found {
		return false
	}
	for _, a := range c.approvals() {
		if a.ThreadID == r.ServerID && sameJSON(a.ID, r.Approval) && approvalFingerprint(a) == r.ApprovalVersion {
			return true
		}
	}
	return false
}

func (d *desktopControl) previewWindow(hint desktopWindowHint) (desktop.Window, []byte, error) {
	if !d.work.TryLock() {
		return desktop.Window{}, nil, errors.New("Refreshing the preview. Try again shortly.")
	}
	defer d.work.Unlock()
	windows, err := d.windows()
	if err != nil {
		return desktop.Window{}, nil, err
	}
	w, err := chooseDesktopWindow(windows, hint)
	if err != nil {
		return w, nil, err
	}
	data, err := d.captureWindow(w)
	if err != nil {
		return w, nil, err
	}
	data, err = phonePreviewData(data)
	return w, data, err
}

func (c *chatController) sendPhoneDesktopPreview(ctx context.Context, r remote.Request, allowed bool, hint desktopWindowHint, send func(remote.Frame) bool) {
	p := phoneDesktopPreview{RequestID: r.RequestID, Approval: r.Approval}
	var err error
	if !allowed {
		err = errors.New("This permission request was already handled or belongs to another session")
	} else {
		p.Window, p.Data, err = c.desktopControls().previewWindow(hint)
		if err == nil {
			p.Dialog, _ = c.desktopControls().dialog(p.Window)
		}
	}
	if err == nil {
		live, alive := onMain(ctx, c.dispatch, func() bool { return c.phoneDesktopApproval(r) })
		if !alive {
			return
		}
		if !live {
			p.Data = nil
			p.Dialog = desktop.Dialog{}
			p.Window = desktop.Window{}
			err = errors.New("Permission request already handled")
		}
	}
	data, _ := json.Marshal(p)
	f := remote.Frame{Type: "desktop-preview", Data: data}
	if err != nil {
		f.Error = err.Error()
	}
	send(f)
}
