package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"miya/internal/desktop"
)

func applicationTestDesktop(t *testing.T) (*desktopControl, string, *atomic.Int32) {
	t.Helper()
	d, token := pickTestDesktop(t, true)
	calls := new(atomic.Int32)
	d.apps = func() ([]desktop.Application, error) {
		return []desktop.Application{{ID: "/Applications/Notes.app", Name: "Notes", BundleID: "com.apple.Notes", Icon: []byte("png")}}, nil
	}
	d.openApp = func(ctx context.Context, id string) error {
		if id != "/Applications/Notes.app" {
			t.Error("wrong app", id)
		}
		calls.Add(1)
		return ctx.Err()
	}
	return d, token, calls
}

func TestDesktopApplicationsAreLeaseBoundAndValidateIDs(t *testing.T) {
	d, token, calls := applicationTestDesktop(t)
	for _, tc := range []struct {
		token, method, path, body string
		status                    int
	}{
		{"wrong", "GET", "/apps", "", 410},
		{token, "POST", "/apps", "{}", 405},
		{token, "GET", "/launch", "", 405},
		{token, "POST", "/launch", `{}`, 400},
		{token, "POST", "/launch", `{"id":"/Applications/Notes.app","args":[]}`, 400},
		{token, "POST", "/launch", `{"id":"/Applications/Notes.app"}{}`, 400},
		{token, "POST", "/launch", `{"id":"https://example.com"}`, 404},
		{token, "POST", "/launch", `{"id":"/Applications/../Applications/Notes.app"}`, 404},
		{token, "POST", "/launch", `{"id":"/Applications/Missing.app"}`, 404},
	} {
		if w := desktopHTTP(d, tc.token, tc.method, tc.path, tc.body); w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid request launched an app")
	}
	var session desktopSession
	if w := desktopHTTP(d, token, "GET", "/session", ""); json.Unmarshal(w.Body.Bytes(), &session) != nil || !session.Apps {
		t.Fatal("missing app capability", w.Body.String())
	}
	d.mu.Lock()
	d.lease.pick = false
	d.mu.Unlock()
	for _, path := range []string{"/apps", "/launch"} {
		method := "GET"
		if path == "/launch" {
			method = "POST"
		}
		if w := desktopHTTP(d, token, method, path, `{"id":"/Applications/Notes.app"}`); w.Code != 403 {
			t.Fatal("authorization lease widened", w.Code)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("authorization lease launched an app")
	}
}

func TestDesktopApplicationsClientListAndLaunch(t *testing.T) {
	d, token, calls := applicationTestDesktop(t)
	c := testDesktopClient(t, d, token)
	ctx := context.Background()
	apps, err := c.applications(ctx)
	if err != nil || len(apps) != 1 || string(apps[0].Icon) != "png" {
		t.Fatal(apps, err)
	}
	app, err := c.launchApplication(ctx, apps[0].ID)
	if err != nil || app.BundleID != apps[0].BundleID || len(app.Icon) != 0 || calls.Load() != 1 {
		t.Fatal(app, err, calls.Load())
	}
	if _, _, err := c.frame(ctx); desktopStatus(err) != 403 {
		t.Fatal("launch started capture implicitly", err)
	}
	c.stop()
	if _, err := c.launchApplication(ctx, apps[0].ID); desktopStatus(err) != 410 || calls.Load() != 1 {
		t.Fatal("launch after stop", err)
	}
}

func TestDesktopApplicationsRejectLockedBusyAndMissingPermission(t *testing.T) {
	for _, state := range []string{"locked", "unknown", "unlocking", "permission", "removed"} {
		t.Run(state, func(t *testing.T) {
			d, token, calls := applicationTestDesktop(t)
			want := 409
			switch state {
			case "locked":
				d.locked = func() (bool, error) { return true, nil }
				want = 423
			case "unknown":
				d.locked = func() (bool, error) { return false, errors.New("unknown") }
				want = 503
			case "unlocking":
				d.lease.unlocking = true
			case "permission":
				d.authorize = func() error { return errors.New("permission") }
				want = 403
			case "removed":
				d.apps = func() ([]desktop.Application, error) { return nil, nil }
				want = 404
			}
			w := desktopHTTP(d, token, "POST", "/launch", `{"id":"/Applications/Notes.app"}`)
			if w.Code != want || calls.Load() != 0 {
				t.Fatal(state, w.Code, calls.Load())
			}
		})
	}
}

func TestDesktopApplicationsRecheckLeaseAfterListing(t *testing.T) {
	for _, path := range []string{"/apps", "/launch"} {
		t.Run(path, func(t *testing.T) {
			d, token, calls := applicationTestDesktop(t)
			list := d.apps
			d.apps = func() ([]desktop.Application, error) { d.revoke(token); return list() }
			method := "GET"
			if path == "/launch" {
				method = "POST"
			}
			if w := desktopHTTP(d, token, method, path, `{"id":"/Applications/Notes.app"}`); w.Code != 410 || calls.Load() != 0 {
				t.Fatal(w.Code, calls.Load())
			}
		})
	}
}

func TestDesktopApplicationsCancelLaunchOnRevoke(t *testing.T) {
	d, token, _ := applicationTestDesktop(t)
	started, done := make(chan struct{}), make(chan int, 1)
	d.openApp = func(ctx context.Context, _ string) error { close(started); <-ctx.Done(); return ctx.Err() }
	go func() { done <- desktopHTTP(d, token, "POST", "/launch", `{"id":"/Applications/Notes.app"}`).Code }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("launch did not start")
	}
	d.revoke(token)
	select {
	case code := <-done:
		if code != 410 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("revocation did not cancel launch")
	}
}
