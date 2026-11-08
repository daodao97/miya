package control

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func leaseClock(t *testing.T) (*Server, string, func(time.Duration)) {
	t.Helper()
	d, token, _ := mockDesktop(t)
	// Install the clock before opening the lease so no real timer or context
	// deadline needs to elapse. Only lease time changes; native work stays fake.
	d.Revoke(token)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	token, err := d.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Revoke(token) })
	return d, token, func(delta time.Duration) { d.mu.Lock(); now = now.Add(delta); d.mu.Unlock() }
}

func TestDesktopLiveViewingRenewsIdleLease(t *testing.T) {
	d, token, advance := leaseClock(t)
	if _, fixed := d.lease.ctx.Deadline(); fixed {
		t.Fatal("viewing still has a fixed deadline")
	}
	if w := desktopHTTP(d, token, "POST", "/start", "{}"); w.Code != 204 {
		t.Fatal(w.Code)
	}
	advance(11 * time.Minute)
	if w := desktopHTTP(d, token, "GET", "/frame", ""); w.Code != 200 {
		t.Fatal("live frame ended after ten minutes", w.Code)
	}
	l := d.lease
	for range 4 {
		advance(25 * time.Minute)
		if w := desktopHTTP(d, token, "GET", "/session", ""); w.Code != 200 {
			t.Fatal("active view expired", w.Code)
		}
		// Simulate a timer callback already queued before the latest renewal.
		d.expireLease(l)
		if !d.Holds(token) || l.ctx.Err() != nil {
			t.Fatal("old timer ended a renewed view")
		}
	}
	advance(desktopControlIdleTimeout)
	d.expireLease(l)
	select {
	case <-l.ctx.Done():
	default:
		t.Fatal("idle lease context was not canceled")
	}
	if d.Holds(token) || d.Busy() {
		t.Fatal("idle lease still holds desktop")
	}
	if w := desktopHTTP(d, token, "GET", "/frame", ""); w.Code != 410 {
		t.Fatal("expired token was revived", w.Code)
	}
}

func TestDesktopInvalidRequestsAndStatusChecksDoNotRenew(t *testing.T) {
	d, token, advance := leaseClock(t)
	l := d.lease
	deadline := l.expires
	advance(20 * time.Minute)
	for _, bad := range []string{"", "wrong"} {
		if w := desktopHTTP(d, bad, "GET", "/session", ""); w.Code != 410 {
			t.Fatal(w.Code)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", Prefix+"/session", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+token)
	d.ServeHTTP(httptest.NewRecorder(), r)
	if !d.Holds(token) || !d.Busy() {
		t.Fatal("valid lease disappeared")
	}
	if !l.expires.Equal(deadline) {
		t.Fatal("invalid request or status check extended lease")
	}
	advance(10 * time.Minute)
	// An authenticated request arriving at expiry cannot renew the lease.
	if w := desktopHTTP(d, token, "GET", "/session", ""); w.Code != 410 {
		t.Fatal("late request revived lease", w.Code)
	}
	d.expireLease(l)
	if d.lease != nil || l.ctx.Err() == nil {
		t.Fatal("idle lease was not cleaned up")
	}
}

func TestDesktopOldIdleTimerCannotEndReplacement(t *testing.T) {
	d, token, advance := leaseClock(t)
	old := d.lease
	d.Revoke(token)
	next, err := d.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Revoke(next) })
	advance(time.Minute)
	d.expireLease(old)
	if !d.Holds(next) {
		t.Fatal("old timer ended replacement lease")
	}
	d.Revoke(next)
	if w := desktopHTTP(d, next, "GET", "/session", ""); w.Code != 410 {
		t.Fatal("closed lease renewed", w.Code)
	}
}
