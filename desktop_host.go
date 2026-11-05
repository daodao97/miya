package main

import (
	"context"
	"encoding/json"
	"errors"
	"miya/internal/desktop"
	"miya/internal/remote"
	"time"
)

// The controller side of remote desktop control: leases for phones, and
// the approvals and previews of window actions they ask for.

func (c *chatController) desktopControls() *desktopControl {
	c.desktopMu.Lock()
	defer c.desktopMu.Unlock()
	if c.desktop == nil {
		c.desktop = &desktopControl{authorize: desktop.Authorize, capture: desktop.Capture, input: desktop.SendInput, windows: desktop.Windows, apps: desktop.Applications, openApp: desktop.OpenApplication, privacy: desktop.StartPrivacy, captureWindow: desktop.CaptureWindow, inputWindow: desktop.SendWindowInput, dialog: desktop.WindowDialog, pressAction: desktop.PressWindowAction, wake: desktop.WakeDisplay, locked: desktop.ScreenLocked}
		c.desktop.displays, c.desktop.captureDisplay, c.desktop.inputDisplay = desktop.Displays, desktop.CaptureDisplay, desktop.SendDisplayInput
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
