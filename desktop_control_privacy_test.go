package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"miya/internal/desktop"
)

type testDesktopPrivacy struct {
	done chan struct{}
	once sync.Once
}

func (p *testDesktopPrivacy) Done() <-chan struct{} { return p.done }
func (p *testDesktopPrivacy) Close() error          { p.once.Do(func() { close(p.done) }); return nil }

func privacyTestDesktop(t *testing.T) (*desktopControl, string, *atomic.Int32, **testDesktopPrivacy) {
	t.Helper()
	d, token := pickTestDesktop(t, true)
	var current *testDesktopPrivacy
	calls := new(atomic.Int32)
	d.privacy = func(ctx context.Context) (desktop.Privacy, error) {
		calls.Add(1)
		p := &testDesktopPrivacy{done: make(chan struct{})}
		current = p
		go func() {
			select {
			case <-ctx.Done():
				_ = p.Close()
			case <-p.done:
			}
		}()
		return p, nil
	}
	return d, token, calls, &current
}

func readyPrivacyDesktop(t *testing.T, d *desktopControl, token string) {
	t.Helper()
	// Select the full display and receive a real frame before allowing blackout.
	if w := desktopHTTP(d, token, "POST", "/select", `{"id":0,"pid":0}`); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := desktopHTTP(d, token, "POST", "/start", `{}`); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := desktopHTTP(d, token, "GET", "/frame", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func TestDesktopPrivacyLeaseAndValidation(t *testing.T) {
	d, token, calls, _ := privacyTestDesktop(t)
	for _, tc := range []struct {
		token, method, body string
		status              int
	}{
		{"wrong", "GET", "", 410}, {token, "DELETE", "", 405},
		{token, "POST", `{}`, 400}, {token, "POST", `{"enabled":null}`, 400},
		{token, "POST", `{"enabled":true,"extra":1}`, 400}, {token, "POST", `{"enabled":true}{}`, 400},
		{token, "POST", `{"enabled":true}`, 409},
	} {
		if w := desktopHTTP(d, tc.token, tc.method, "/privacy", tc.body); w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	d.mu.Lock()
	d.lease.pick = false
	d.mu.Unlock()
	for _, method := range []string{"GET", "POST"} {
		if w := desktopHTTP(d, token, method, "/privacy", `{"enabled":true}`); w.Code != 403 {
			t.Fatal("approval lease changed privacy", w.Code)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid request blanked the display")
	}
}

func TestDesktopPrivacyPersistsUntilDisabledOrRevoked(t *testing.T) {
	for _, end := range []string{"disable", "revoke", "local shortcut"} {
		t.Run(end, func(t *testing.T) {
			d, token, calls, current := privacyTestDesktop(t)
			readyPrivacyDesktop(t, d, token)
			client := testDesktopClient(t, d, token)
			on, off := true, false
			state, err := client.privacy(context.Background(), &on)
			if err != nil || !state.Enabled {
				t.Fatal(state, err)
			}
			select {
			case <-(*current).done:
				t.Fatal("HTTP response ending restored the displays")
			default:
			}
			state, err = client.privacy(context.Background(), &on)
			if err != nil || !state.Enabled || calls.Load() != 1 {
				t.Fatal("enable not idempotent", state, err, calls.Load())
			}
			switch end {
			case "disable":
				state, err = client.privacy(context.Background(), &off)
				if err != nil || state.Enabled {
					t.Fatal(state, err)
				}
			case "revoke":
				d.revoke(token)
			case "local shortcut":
				_ = (*current).Close()
			}
			select {
			case <-(*current).done:
			case <-time.After(time.Second):
				t.Fatal("display restore not requested")
			}
			state, err = client.privacy(context.Background(), nil)
			if end == "revoke" {
				if desktopStatus(err) != 410 {
					t.Fatal(err)
				}
			} else if err != nil || state.Enabled {
				t.Fatal("ended helper reported enabled", state, err)
			}
		})
	}
}

func TestDesktopPrivacyUnavailableAndLockedDoNotEnable(t *testing.T) {
	for _, condition := range []string{"locked", "unknown lock", "busy", "stale", "unsupported", "helper ended"} {
		t.Run(condition, func(t *testing.T) {
			d, token, calls, _ := privacyTestDesktop(t)
			readyPrivacyDesktop(t, d, token)
			want := 503
			switch condition {
			case "locked":
				d.locked = func() (bool, error) { return true, nil }
				want = 423
			case "unknown lock":
				d.locked = func() (bool, error) { return false, errors.New("unknown") }
			case "busy":
				d.lease.unlocking = true
				want = 409
			case "stale":
				d.lease.capturedAt = time.Now().Add(-11 * time.Second)
				want = 409
			case "unsupported":
				d.privacy = func(context.Context) (desktop.Privacy, error) { return nil, errors.New("unsupported") }
			case "helper ended":
				d.privacy = func(context.Context) (desktop.Privacy, error) {
					p := &testDesktopPrivacy{done: make(chan struct{})}
					_ = p.Close()
					return p, nil
				}
			}
			if w := desktopHTTP(d, token, "POST", "/privacy", `{"enabled":true}`); w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			if calls.Load() != 0 {
				t.Fatal("unavailable display blanked")
			}
		})
	}
}

func TestDesktopPrivacyRevokeCancelsStartupWithoutBlockingStop(t *testing.T) {
	d, token, _, _ := privacyTestDesktop(t)
	readyPrivacyDesktop(t, d, token)
	started, result := make(chan struct{}), make(chan int, 1)
	d.privacy = func(ctx context.Context) (desktop.Privacy, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	go func() { result <- desktopHTTP(d, token, "POST", "/privacy", `{"enabled":true}`).Code }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("helper did not start")
	}
	if w := desktopHTTP(d, token, "POST", "/stop", `{}`); w.Code != http.StatusNoContent {
		t.Fatal(w.Code)
	}
	select {
	case code := <-result:
		if code != 410 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("startup outlived lease")
	}
}
