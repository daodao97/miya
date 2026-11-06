package control

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"miya/internal/desktop"
)

func desktopHTTP(d *Server, token, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, Prefix+path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	return w
}
func mockDesktop(t *testing.T) (*Server, string, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	d := &Server{Native: Native{Authorize: func() error { return nil }, Capture: func() ([]byte, error) { return []byte("jpeg"), nil }, Input: func(desktop.Input) error { calls.Add(1); return nil }}}
	token, err := d.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Revoke(token) })
	return d, token, calls
}
func TestDesktopConsentRevocationAndValidation(t *testing.T) {
	d, token, calls := mockDesktop(t)
	for _, tok := range []string{"", "other"} {
		if w := desktopHTTP(d, tok, "GET", "/frame", ""); w.Code != 410 {
			t.Fatalf("unauthorized: %d", w.Code)
		}
	}
	if w := desktopHTTP(d, token, "GET", "/frame", ""); w.Code != 403 {
		t.Fatal("captured before consent")
	}
	if w := desktopHTTP(d, token, "POST", "/input", `{"kind":"click"}`); w.Code != 403 {
		t.Fatal("input before consent")
	}
	if w := desktopHTTP(d, token, "POST", "/start", `{}`); w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	if w := desktopHTTP(d, token, "POST", "/input", `{"kind":"click"}`); w.Code != 409 {
		t.Fatal("input allowed without a current frame")
	}
	if w := desktopHTTP(d, token, "GET", "/frame", ""); w.Code != 200 || w.Body.String() != "jpeg" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("capture: %d %s", w.Code, w.Body)
	}
	if w := desktopHTTP(d, token, "GET", "/frame", ""); w.Code != 429 {
		t.Fatal("unbounded capture rate")
	}
	for _, body := range []string{`{"kind":"shell"}`, `{"kind":"click","x":2}`, `{"kind":"key","key":"bad"}`, `{"kind":"text","text":""}`, `{"kind":"click","extra":true}`} {
		if w := desktopHTTP(d, token, "POST", "/input", body); w.Code != 400 {
			t.Fatalf("invalid input accepted: %s %d", body, w.Code)
		}
	}
	if w := desktopHTTP(d, token, "POST", "/input", `{"kind":"text","text":"中文 🐈"}`); w.Code != 204 || calls.Load() != 1 {
		t.Fatalf("input: %d, calls %d", w.Code, calls.Load())
	}
	if w := desktopHTTP(d, token, "POST", "/stop", `{}`); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := desktopHTTP(d, token, "POST", "/input", `{"kind":"click"}`); w.Code != 410 || calls.Load() != 1 {
		t.Fatal("input after revocation")
	}
}
func TestDesktopLeaseIsolationDisconnectAndExpiry(t *testing.T) {
	d, token, _ := mockDesktop(t)
	if _, err := d.Open(context.Background()); err == nil {
		t.Fatal("concurrent operator accepted")
	}
	d.Revoke("wrong")
	if w := desktopHTTP(d, token, "POST", "/start", `{}`); w.Code != 204 {
		t.Fatal("wrong token revoked lease")
	}
	d.Revoke(token)
	ctx, cancel := context.WithCancel(context.Background())
	next, err := d.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if w := desktopHTTP(d, next, "POST", "/start", `{}`); w.Code != 410 {
		t.Fatal("lease survived disconnect")
	}
	last, err := d.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Revoke(last)
	if w := desktopHTTP(d, token, "POST", "/start", `{}`); w.Code != 410 {
		t.Fatal("old token reached new lease")
	}
	d.mu.Lock()
	d.lease.expires = time.Now().Add(-time.Second)
	d.mu.Unlock()
	if w := desktopHTTP(d, last, "GET", "/frame", ""); w.Code != 410 {
		t.Fatal("lease survived expiry")
	}
}
