// Package controltest builds desktop control servers and clients backed by
// fakes, for tests outside package control. The fixtures mirror the ones
// package control's own tests use, through its exported API only.
package controltest

import (
	"context"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
)

func mock(t *testing.T) (*control.Server, string, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	d := &control.Server{Authorize: func() error { return nil }, Capture: func() ([]byte, error) { return []byte("jpeg"), nil }, Input: func(desktop.Input) error { calls.Add(1); return nil }}
	token, err := d.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Revoke(token) })
	return d, token, calls
}

func do(d *control.Server, token, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, control.Prefix+path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	return w
}

func pick(t *testing.T, pick bool) (*control.Server, string) {
	t.Helper()
	d, token, _ := mock(t)
	d.Windows = func() ([]desktop.Window, error) {
		return []desktop.Window{{ID: 5, PID: 9, App: "Notes", Title: "Draft"}}, nil
	}
	d.CaptureWindow = func(desktop.Window) ([]byte, error) { return []byte("window"), nil }
	d.SetLeaseWindow(&desktop.Window{})
	if pick {
		d.AllowPick(token)
	}
	return d, token
}

func Display(t *testing.T) (*control.Server, string, []desktop.Display, *atomic.Uint32) {
	t.Helper()
	d, token := pick(t, true)
	displays := []desktop.Display{{ID: 1, UUID: "built-in", Name: "Built-in Retina Display", Main: true, Width: 1512, Height: 982}, {ID: 3, UUID: "external", Name: "DELL P2725QE", Width: 2560, Height: 1440}}
	input := new(atomic.Uint32)
	d.Displays = func() ([]desktop.Display, error) { return displays, nil }
	d.CaptureDisplay = func(display desktop.Display) ([]byte, error) { return []byte(display.Name), nil }
	d.InputDisplay = func(display desktop.Display, in desktop.Input) error { input.Store(display.ID); return nil }
	return d, token, displays, input
}

type PrivacySession struct {
	done chan struct{}
	once sync.Once
}

func (p *PrivacySession) Done() <-chan struct{} { return p.done }
func (p *PrivacySession) Close() error          { p.once.Do(func() { close(p.done) }); return nil }

func Privacy(t *testing.T) (*control.Server, string, *atomic.Int32, **PrivacySession) {
	t.Helper()
	d, token := pick(t, true)
	var current *PrivacySession
	calls := new(atomic.Int32)
	d.Privacy = func(ctx context.Context) (desktop.Privacy, error) {
		calls.Add(1)
		p := &PrivacySession{done: make(chan struct{})}
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

func ReadyPrivacy(t *testing.T, d *control.Server, token string) {
	t.Helper()
	// Select the full display and receive a real frame before allowing blackout.
	if w := do(d, token, "POST", "/select", `{"id":0,"pid":0}`); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := do(d, token, "POST", "/start", `{}`); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := do(d, token, "GET", "/frame", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func Application(t *testing.T) (*control.Server, string, *atomic.Int32) {
	t.Helper()
	d, token := pick(t, true)
	calls := new(atomic.Int32)
	d.Apps = func() ([]desktop.Application, error) {
		return []desktop.Application{{ID: "/Applications/Notes.app", Name: "Notes", BundleID: "com.apple.Notes", Icon: []byte("png")}}, nil
	}
	d.OpenApp = func(ctx context.Context, id string) error {
		if id != "/Applications/Notes.app" {
			t.Error("wrong app", id)
		}
		calls.Add(1)
		return ctx.Err()
	}
	return d, token, calls
}

func Client(t *testing.T, d *control.Server, token string) *control.Client {
	t.Helper()
	server := httptest.NewServer(d)
	t.Cleanup(server.Close)
	addr := server.Listener.Addr().String()
	return control.NewClient(func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}, token)
}
