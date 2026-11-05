package main

import (
	"encoding/json"
	"errors"
	"strings"

	"miya/internal/desktop"
	"miya/internal/thumbnail"
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
	data, err = thumbnail.Data(data)
	return w, data, err
}
