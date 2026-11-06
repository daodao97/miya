package controller

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"miya/internal/async"
	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/phone"
	"miya/internal/phoneapi"
	"miya/internal/remote"
)

func (c *Controller) desktopControls() *control.Server {
	c.desktopMu.Lock()
	defer c.desktopMu.Unlock()
	if c.Desktop == nil {
		c.Desktop = &control.Server{Native: control.Native{Authorize: desktop.Authorize, Capture: desktop.Capture, Input: desktop.SendInput, Windows: desktop.Windows, Apps: desktop.Applications, OpenApp: desktop.OpenApplication, Privacy: desktop.StartPrivacy, CaptureWindow: desktop.CaptureWindow, InputWindow: desktop.SendWindowInput, Dialog: desktop.WindowDialog, PressAction: desktop.PressWindowAction, Wake: desktop.WakeDisplay, Locked: desktop.ScreenLocked}}
		c.Desktop.Displays, c.Desktop.CaptureDisplay, c.Desktop.InputDisplay = desktop.Displays, desktop.CaptureDisplay, desktop.SendDisplayInput
	}
	return c.Desktop
}

func (c *Controller) desktopBusy() bool {
	c.desktopMu.Lock()
	d := c.Desktop
	c.desktopMu.Unlock()
	if d == nil {
		return false
	}
	return d.Busy()
}

// Button titles are display data, never outcome classifiers. An observed native
// action completes a takeover only after its exact window stays gone, with no
// successor window from the same owner appearing during the settling interval.
func (c *Controller) ConfirmDesktopAction(ctx context.Context, r remote.Request, action desktop.Action, before []desktop.Window) {
	if r.WindowID == 0 || r.WindowPID <= 0 || len(before) == 0 {
		return
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	var absentSince time.Time
	for {
		windows, err := c.desktopControls().Windows()
		if err != nil {
			// A failed read is not evidence that the dialog closed.
			absentSince = time.Time{}
		} else {
			present, successor := control.ActionWindows(r.WindowID, r.WindowPID, before, windows)
			if successor {
				return // Another step needs the user's input; retain the approval.
			}
			if present {
				absentSince = time.Time{}
			} else if absentSince.IsZero() {
				absentSince = time.Now()
			} else if time.Since(absentSince) >= 300*time.Millisecond {
				_, _ = async.OnMain(ctx, c.Dispatch, func() bool { c.CompleteDesktopAction(r, action); return true })
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

func (c *Controller) sendPhoneDesktopAction(ctx context.Context, r remote.Request, allowed bool, send func(remote.Frame) bool) {
	p := control.Preview{RequestID: r.RequestID, Approval: r.Approval}
	var err error
	if allowed {
		var alive bool
		allowed, alive = async.OnMain(ctx, c.Dispatch, func() bool { return c.PhoneDesktopApproval(r) })
		if !alive {
			return
		}
	}
	if !allowed {
		err = errors.New("This permission request was already handled or belongs to another session")
	} else {
		var action desktop.Action
		var before []desktop.Window
		action, before, err = c.desktopControls().PerformWindowAction(desktop.Window{ID: r.WindowID, PID: r.WindowPID}, r.DesktopAction)
		if err == nil {
			c.ConfirmDesktopAction(ctx, r, action, before)
		}
	}
	data, _ := json.Marshal(p)
	f := remote.Frame{Type: "desktop-action", Data: data}
	if err != nil {
		f.Error = err.Error()
	}
	send(f)
}

func (c *Controller) sendPhoneDesktopPreview(ctx context.Context, r remote.Request, allowed bool, hint control.WindowHint, send func(remote.Frame) bool) {
	p := control.Preview{RequestID: r.RequestID, Approval: r.Approval}
	var err error
	if !allowed {
		err = errors.New("This permission request was already handled or belongs to another session")
	} else {
		p.Window, p.Data, err = c.desktopControls().PreviewWindow(hint)
		if err == nil {
			p.Dialog, _ = c.desktopControls().Dialog(p.Window)
		}
	}
	if err == nil {
		live, alive := async.OnMain(ctx, c.Dispatch, func() bool { return c.PhoneDesktopApproval(r) })
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
func (c *Controller) PhoneDesktopApproval(r remote.Request) bool {
	if r.ServerID == "" || !c.PhoneThreadFor(r).Found {
		return false
	}
	for _, a := range c.Approvals() {
		if a.ThreadID == r.ServerID && phone.SameJSON(a.ID, r.Approval) && phoneapi.ApprovalFingerprint(a) == r.ApprovalVersion {
			return true
		}
	}
	return false
}

func (c *Controller) phoneDesktopHint(r remote.Request) control.WindowHint {
	if r.WindowID != 0 && r.WindowPID > 0 {
		return control.WindowHint{Window: &desktop.Window{ID: r.WindowID, PID: r.WindowPID}}
	}
	for _, a := range c.Approvals() {
		if a.Method == "miya/desktopControl" && phone.SameJSON(a.ID, r.Approval) {
			var hint control.WindowHint
			_ = json.Unmarshal(a.Payload, &hint)
			return hint
		}
	}
	return control.WindowHint{}
}

func (c *Controller) CompleteDesktopAction(r remote.Request, action desktop.Action) {
	if !c.PhoneDesktopApproval(r) {
		return
	}
	for _, a := range c.Approvals() {
		if phone.SameJSON(a.ID, r.Approval) && a.Method == "miya/desktopControl" {
			_ = c.PhoneApprovalResponse(remote.Request{Approval: a.ID, ApprovalVersion: r.ApprovalVersion, Allow: !action.Cancel})
			return
		}
	}
}
