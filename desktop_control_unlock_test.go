package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"miya/internal/desktop"
)

func startUnlockDesktop(t *testing.T) (*desktopControl, string, *atomic.Int32) {
	t.Helper()
	d, token, calls := mockDesktop(t)
	d.locked = func() (bool, error) { return true, nil }
	if w := desktopHTTP(d, token, "POST", "/start", "{}"); w.Code != 204 {
		t.Fatal(w.Code)
	}
	return d, token, calls
}

func TestDesktopUnlockGatesAndValidation(t *testing.T) {
	d, token, calls := mockDesktop(t)
	d.locked = func() (bool, error) { return true, nil }
	if w := desktopHTTP(d, token, "POST", "/unlock", `{"password":"secret"}`); w.Code != 403 {
		t.Fatal("unlock before start", w.Code)
	}
	desktopHTTP(d, token, "POST", "/start", "{}")
	for _, body := range []string{`{}`, `{"password":""}`, `{"password":"x\n"}`, `{"password":"x\r"}`, `{"password":"x\u0000"}`, `{"password":"secret","extra":1}`, `{"password":"secret"} {}`, `{"password":123}`, `{"password":"` + strings.Repeat("a", 257) + `"}`} {
		if w := desktopHTTP(d, token, "POST", "/unlock", body); w.Code != 400 || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("invalid body: status %d", w.Code)
		}
	}
	if w := desktopHTTP(d, token, "GET", "/unlock", ""); w.Code != 405 {
		t.Fatal("GET unlock", w.Code)
	}
	d.locked = nil
	if w := desktopHTTP(d, token, "POST", "/unlock", `{"password":"secret"}`); w.Code != 403 {
		t.Fatal("unlock without lock detector", w.Code)
	}
	d.locked = func() (bool, error) { return false, nil }
	if w := desktopHTTP(d, token, "POST", "/unlock", `{"password":"secret"}`); w.Code != 409 {
		t.Fatal("already unlocked", w.Code)
	}
	d.locked = func() (bool, error) { return false, errors.New("private details") }
	if w := desktopHTTP(d, token, "POST", "/unlock", `{"password":"secret"}`); w.Code != 503 || strings.Contains(w.Body.String(), "private") {
		t.Fatal("unknown lock state", w.Code)
	}
	if calls.Load() != 0 {
		t.Fatal("rejected requests sent input")
	}
}

func TestDesktopUnlockAuthorizationWindowDenied(t *testing.T) {
	d, token, calls := startUnlockDesktop(t)
	d.mu.Lock()
	d.lease.window = &desktop.Window{ID: 42, PID: 7}
	d.mu.Unlock()
	if w := desktopHTTP(d, token, "POST", "/unlock", `{"password":"secret"}`); w.Code != 403 || calls.Load() != 0 {
		t.Fatal("authorization lease unlocked desktop", w.Code)
	}
}

func TestDesktopUnlockSuccessAndIncorrectPassword(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "incorrect"}[success], func(t *testing.T) {
			d, token, _ := startUnlockDesktop(t)
			locked := true
			var inputs []desktop.Input
			d.locked = func() (bool, error) { return locked, nil }
			d.input = func(in desktop.Input) error {
				if err := in.Validate(); err != nil {
					t.Fatal(err)
				}
				inputs = append(inputs, in)
				if in.Key == "enter" && success {
					locked = false
				}
				return nil
			}
			w := desktopHTTP(d, token, "POST", "/unlock", `{"password":"中文-test"}`)
			want := http.StatusUnauthorized
			if success {
				want = http.StatusNoContent
			}
			if w.Code != want || len(inputs) != 3 || inputs[0].Key != "a" || inputs[0].Modifier != "command" || inputs[1].Text != "中文-test" || inputs[2].Key != "enter" {
				t.Fatalf("unlock status %d; input count %d", w.Code, len(inputs))
			}
			if strings.Contains(w.Body.String(), "中文-test") {
				t.Fatal("password echoed")
			}
		})
	}
}

func TestDesktopUnlockRechecksAfterWakeAndInput(t *testing.T) {
	for _, unlockAt := range []string{"wake", "select", "text"} {
		t.Run(unlockAt, func(t *testing.T) {
			d, token, _ := startUnlockDesktop(t)
			locked, calls := true, 0
			d.locked = func() (bool, error) { return locked, nil }
			d.wake = func() error {
				if unlockAt == "wake" {
					locked = false
				}
				return nil
			}
			d.input = func(in desktop.Input) error {
				calls++
				if unlockAt == "select" || unlockAt == "text" && in.Kind == "text" {
					locked = false
				}
				return nil
			}
			w := desktopHTTP(d, token, "POST", "/unlock", `{"password":"secret"}`)
			want := map[string]int{"wake": 0, "select": 1, "text": 2}[unlockAt]
			if w.Code != 409 || calls != want {
				t.Fatalf("status %d calls %d", w.Code, calls)
			}
		})
	}
}

func TestDesktopUnlockRevocationAndConcurrentInput(t *testing.T) {
	d, token, calls := startUnlockDesktop(t)
	waking := make(chan struct{}, 1)
	d.wake = func() error { waking <- struct{}{}; return nil }
	result := make(chan int, 1)
	go func() { result <- desktopHTTP(d, token, "POST", "/unlock", `{"password":"secret"}`).Code }()
	select {
	case <-waking:
	case <-time.After(time.Second):
		t.Fatal("unlock did not start")
	}
	if w := desktopHTTP(d, token, "POST", "/unlock", `{"password":"secret"}`); w.Code != 429 {
		t.Fatal("parallel unlock", w.Code)
	}
	if w := desktopHTTP(d, token, "POST", "/input", `{"kind":"text","text":"other"}`); w.Code != 409 {
		t.Fatal("interleaved input", w.Code)
	}
	desktopHTTP(d, token, "POST", "/stop", "{}")
	select {
	case code := <-result:
		if code != 410 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("revocation did not cancel unlock")
	}
	if calls.Load() != 0 {
		t.Fatal("input after revocation")
	}
}

func TestDesktopUnlockInputErrorIsRedacted(t *testing.T) {
	d, token, _ := startUnlockDesktop(t)
	d.input = func(desktop.Input) error { return errors.New("native secret") }
	w := desktopHTTP(d, token, "POST", "/unlock", `{"password":"secret"}`)
	if w.Code != 403 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal("native input error exposed", w.Code)
	}
}

func TestDesktopUnlockLockStateWithoutCapture(t *testing.T) {
	for _, window := range []bool{false, true} {
		d, token, _ := startUnlockDesktop(t)
		d.capture = func() ([]byte, error) { t.Error("captured while locked"); return nil, nil }
		d.captureWindow = func(desktop.Window) ([]byte, error) { t.Error("captured locked window"); return nil, nil }
		if window {
			d.lease.window = &desktop.Window{ID: 1, PID: 2}
			d.lease.pick = true
		}
		client := testDesktopClient(t, d, token)
		_, locked, err := client.frame(context.Background())
		if !locked || desktopStatus(err) != 423 {
			t.Fatal("lost lock state without a frame", locked, err)
		}
	}
}
