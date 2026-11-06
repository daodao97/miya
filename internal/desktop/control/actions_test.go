package control

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"miya/internal/desktop"
)

func TestDesktopLeaseActionsRecheckNativeStateAndWindow(t *testing.T) {
	window := desktop.Window{ID: 81, PID: 120}
	var enabled atomic.Bool
	var presses atomic.Int32
	entered, release := make(chan desktop.Window, 1), make(chan struct{})
	d := &Server{Native: Native{
		Authorize:     func() error { return nil },
		Windows:       func() ([]desktop.Window, error) { return []desktop.Window{window}, nil },
		CaptureWindow: func(desktop.Window) ([]byte, error) { return []byte("jpeg"), nil },
		Dialog: func(w desktop.Window) (desktop.Dialog, error) {
			if w != window {
				t.Error("dialog escaped the selected window")
			}
			return desktop.Dialog{Actions: []desktop.Action{{ID: "allow", Label: "允许访问", Enabled: enabled.Load()}}}, nil
		},
		PressAction: func(w desktop.Window, id string) error {
			if w != window || id != "allow" {
				t.Error("action escaped the selected window")
			}
			presses.Add(1)
			return nil
		},
	}}
	token, err := d.OpenWindow(context.Background(), &window)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { close(release); d.Revoke(token) })
	d.lease.onAction = func(w desktop.Window, _ desktop.Action, before []desktop.Window) {
		if len(before) != 1 || before[0] != window {
			t.Error("missing pre-action window snapshot")
		}
		entered <- w
		<-release
	}
	body := `{"id":"allow","windowId":81,"windowPid":120}`
	if w := desktopHTTP(d, token, "POST", "/action", body); w.Code != 403 {
		t.Fatal("action before start", w.Code)
	}
	if w := desktopHTTP(d, token, "GET", "/actions", ""); w.Code != 403 {
		t.Fatal("native state before start", w.Code)
	}
	desktopHTTP(d, token, "POST", "/start", `{}`)
	if w := desktopHTTP(d, token, "POST", "/action", body); w.Code != 409 {
		t.Fatal("action without fresh pixels", w.Code)
	}
	desktopHTTP(d, token, "GET", "/frame", "")
	if w := desktopHTTP(d, token, "POST", "/action", body); w.Code != 409 || presses.Load() != 0 {
		t.Fatal("disabled native action submitted", w.Code)
	}
	enabled.Store(true)
	w := desktopHTTP(d, token, "GET", "/actions", "")
	var state struct {
		Window  desktop.Window
		Actions []desktop.Action
		Pending bool
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || len(state.Actions) != 1 || !state.Actions[0].Enabled || state.Window != window {
		t.Fatal("current native buttons not published", w.Code, w.Body)
	}
	for _, bad := range []string{
		`{"id":"allow","windowId":81,"windowPid":999}`,
		`{"id":"unknown","windowId":81,"windowPid":120}`,
		`{"id":"allow"}`,
	} {
		if w := desktopHTTP(d, token, "POST", "/action", bad); w.Code != 409 {
			t.Fatal("stale or unknown native action accepted", w.Code)
		}
	}
	if w := desktopHTTP(d, token, "POST", "/action", `{"id":"allow","windowId":81,"windowPid":120,"text":"unexpected"}`); w.Code != 400 {
		t.Fatal("extra fields accepted", w.Code)
	}
	if w := desktopHTTP(d, token, "POST", "/action", body); w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
	select {
	case got := <-entered:
		if got != window {
			t.Fatal("completion watched the wrong window")
		}
	case <-time.After(time.Second):
		t.Fatal("native action did not start confirmation")
	}
	if w := desktopHTTP(d, token, "POST", "/action", body); w.Code != 409 || presses.Load() != 1 {
		t.Fatal("duplicate native submit accepted", w.Code)
	}
	if w := desktopHTTP(d, token, "POST", "/input", `{"kind":"key","key":"enter"}`); w.Code != 409 && w.Code != 429 {
		t.Fatal("keyboard bypassed pending native submit", w.Code)
	}
	w = desktopHTTP(d, token, "GET", "/actions", "")
	if json.Unmarshal(w.Body.Bytes(), &state) != nil || !state.Pending {
		t.Fatal("pending authorization not published")
	}
	d.Revoke(token)
	if w := desktopHTTP(d, token, "GET", "/actions", ""); w.Code != 410 {
		t.Fatal("native metadata survived revocation")
	}
}

func TestDesktopFullDisplayDoesNotExposeAuthorizationActions(t *testing.T) {
	d, token, _ := mockDesktop(t)
	desktopHTTP(d, token, "POST", "/start", `{}`)
	for _, path := range []string{"/actions", "/action"} {
		method, body := "GET", ""
		if path == "/action" {
			method, body = "POST", `{"id":"allow","windowId":1,"windowPid":2}`
		}
		if w := desktopHTTP(d, token, method, path, body); w.Code != 403 {
			t.Fatal("full desktop exposed a native approval action", path, w.Code)
		}
	}
}
