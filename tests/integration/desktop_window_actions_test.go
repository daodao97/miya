package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	codex "miya/internal/agent/codex/native"
	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/model"
	"miya/internal/phoneapi"
	"miya/internal/remote"
)

func TestDesktopAuthorizationButtonsUseCurrentNativeState(t *testing.T) {
	window := desktop.Window{ID: 81, PID: 120}
	enabled := false
	presses := 0
	d := &control.Server{Native: control.Native{
		Windows: func() ([]desktop.Window, error) { return []desktop.Window{window}, nil },
		Dialog: func(w desktop.Window) (desktop.Dialog, error) {
			if w != window {
				t.Fatal("wrong target")
			}
			return desktop.Dialog{Actions: []desktop.Action{{ID: "允许访问", Label: "允许访问", Enabled: enabled}}}, nil
		},
		PressAction: func(w desktop.Window, id string) error {
			if w != window || id != "允许访问" {
				t.Fatal("wrong action")
			}
			presses++
			return nil
		},
	}}
	if _, _, err := d.PerformWindowAction(window, "允许访问"); err == nil || presses != 0 {
		t.Fatal("disabled password action pressed")
	}
	enabled = true
	if _, _, err := d.PerformWindowAction(window, "unknown"); err == nil || presses != 0 {
		t.Fatal("unadvertised action pressed")
	}
	a, _, err := d.PerformWindowAction(window, "允许访问")
	if err != nil || presses != 1 {
		t.Fatal(err, presses)
	}
	if a.ID != "允许访问" {
		t.Fatal("wrong native action returned")
	}
}

func TestDesktopAuthorizationRejectedPasswordKeepsTakeoverPending(t *testing.T) {
	c := serverController(t.TempDir(), "")
	c.Run = func(f func()) { f() }
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "s"}})
	id, ch := c.AskUser(model.Thread{ID: 1, ServerID: "s"}, "授权", "填写密码")
	list := c.Approvals()
	list[0].Method = "miya/desktopControl"
	c.SetApprovals(list)
	r := remote.Request{ServerID: "s", Approval: id, ApprovalVersion: phoneapi.ApprovalFingerprint(list[0]), WindowID: 81, WindowPID: 120}
	c.Desktop = &control.Server{Native: control.Native{Windows: func() ([]desktop.Window, error) { return []desktop.Window{{ID: 81, PID: 120}}, nil }}}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	c.ConfirmDesktopAction(ctx, r, desktop.Action{Label: "任意文字"}, []desktop.Window{{ID: 81, PID: 120}})
	select {
	case <-ch:
		t.Fatal("visible password window was considered completed")
	default:
	}
	if len(c.Approvals()) != 1 {
		t.Fatal("password rejection removed takeover")
	}
}

func TestDesktopAuthorizationActionCompletesOnlyItsTakeover(t *testing.T) {
	c := serverController(t.TempDir(), "")
	c.Run = func(f func()) { f() }
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "s"}})
	id, ch := c.AskUser(model.Thread{ID: 1, ServerID: "s"}, "授权", "填写密码")
	list := c.Approvals()
	list[0].Method = "miya/desktopControl"
	c.SetApprovals(list)
	a := c.Approvals()[0]
	r := remote.Request{ServerID: "s", Approval: id, ApprovalVersion: phoneapi.ApprovalFingerprint(a)}
	r.ApprovalVersion = "stale"
	c.CompleteDesktopAction(r, desktop.Action{Label: "允许访问"})
	if len(c.Approvals()) != 1 {
		t.Fatal("stale action completed request")
	}
	r.ApprovalVersion = phoneapi.ApprovalFingerprint(a)
	c.CompleteDesktopAction(r, desktop.Action{Label: "允许访问"})
	select {
	case ok := <-ch:
		if !ok {
			t.Fatal("authorization was declined")
		}
	default:
		t.Fatal("native allow did not resolve takeover")
	}
	if len(c.Approvals()) != 0 {
		t.Fatal("completed card retained")
	}
	a = codex.Approval{ID: json.RawMessage(`"question"`), ThreadID: "s", Method: "item/tool/requestUserInput"}
	c.SetApprovals([]codex.Approval{a})
	r.Approval = a.ID
	r.ApprovalVersion = phoneapi.ApprovalFingerprint(a)
	c.CompleteDesktopAction(r, desktop.Action{Label: "允许访问"})
	if len(c.Approvals()) != 1 {
		t.Fatal("native action answered unrelated question")
	}
}

func TestDesktopAuthorizationCompletionDoesNotDependOnButtonText(t *testing.T) {
	for _, tc := range []struct {
		label  string
		cancel bool
	}{
		{"好", false}, {"Weiter", false}, {"次へ", false}, {"任意按钮", false},
		// Deliberately misleading labels: native cancellation still wins.
		{"不允许", false}, {"允许访问", true},
	} {
		t.Run(tc.label, func(t *testing.T) {
			c := serverController(t.TempDir(), "")
			c.Run = func(f func()) { f() }
			c.SetThreads([]model.Thread{{ID: 1, ServerID: "s"}})
			id, ch := c.AskUser(model.Thread{ID: 1, ServerID: "s"}, "接管", "")
			list := c.Approvals()
			list[0].Method = "miya/desktopControl"
			c.SetApprovals(list)
			r := remote.Request{ServerID: "s", Approval: id, ApprovalVersion: phoneapi.ApprovalFingerprint(list[0]), WindowID: 81, WindowPID: 120}
			reads := 0
			c.Desktop = &control.Server{Native: control.Native{Windows: func() ([]desktop.Window, error) {
				reads++
				if reads == 1 {
					return nil, errors.New("temporary window read failure")
				}
				return []desktop.Window{{ID: 99, PID: 321}}, nil // Unrelated app is irrelevant.
			}}}
			ctx, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			c.ConfirmDesktopAction(ctx, r, desktop.Action{ID: "button", Label: tc.label, Cancel: tc.cancel}, []desktop.Window{{ID: 81, PID: 120}})
			select {
			case allow := <-ch:
				if allow == tc.cancel {
					t.Fatal("result did not follow native cancellation semantics")
				}
			default:
				t.Fatal("window closure after native action did not resolve takeover")
			}
		})
	}
}

func TestDesktopAuthorizationTransitionKeepsTakeoverPending(t *testing.T) {
	c := serverController(t.TempDir(), "")
	c.Run = func(f func()) { f() }
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "s"}})
	id, ch := c.AskUser(model.Thread{ID: 1, ServerID: "s"}, "接管", "")
	list := c.Approvals()
	list[0].Method = "miya/desktopControl"
	c.SetApprovals(list)
	r := remote.Request{ServerID: "s", Approval: id, ApprovalVersion: phoneapi.ApprovalFingerprint(list[0]), WindowID: 81, WindowPID: 120}
	reads := 0
	c.Desktop = &control.Server{Native: control.Native{Windows: func() ([]desktop.Window, error) {
		reads++
		if reads == 1 {
			return nil, nil
		} // Brief gap between authorization steps.
		return []desktop.Window{{ID: 82, PID: 120}}, nil
	}}}
	c.ConfirmDesktopAction(context.Background(), r, desktop.Action{Label: "允许访问", Default: true}, []desktop.Window{{ID: 81, PID: 120}})
	select {
	case <-ch:
		t.Fatal("next authorization step was completed prematurely")
	default:
	}
	if len(c.Approvals()) != 1 {
		t.Fatal("transition removed approval")
	}
}

func TestDesktopActionWindowIdentityAndSuccessors(t *testing.T) {
	target := desktop.Window{ID: 81, PID: 120, BundleID: "app.example"}
	existing := desktop.Window{ID: 82, PID: 120, BundleID: "app.example"}
	before := []desktop.Window{target, existing}
	for _, tc := range []struct {
		name               string
		after              []desktop.Window
		present, successor bool
	}{
		{"same window stays", before, true, false},
		{"existing sibling stays", []desktop.Window{existing}, false, false},
		{"new same owner", []desktop.Window{{ID: 83, PID: 120}}, false, true},
		{"new owner same app", []desktop.Window{{ID: 84, PID: 121, BundleID: "app.example"}}, false, true},
		{"reused window number other owner", []desktop.Window{{ID: 81, PID: 999}}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			present, successor := control.ActionWindows(target.ID, target.PID, before, tc.after)
			if present != tc.present || successor != tc.successor {
				t.Fatal(present, successor)
			}
		})
	}
}
