package main

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"miya/internal/desktop"
)

func displayTestDesktop(t *testing.T) (*desktopControl, string, []desktop.Display, *atomic.Uint32) {
	t.Helper()
	d, token := pickTestDesktop(t, true)
	displays := []desktop.Display{{ID: 1, UUID: "built-in", Name: "Built-in Retina Display", Main: true, Width: 1512, Height: 982}, {ID: 3, UUID: "external", Name: "DELL P2725QE", Width: 2560, Height: 1440}}
	input := new(atomic.Uint32)
	d.displays = func() ([]desktop.Display, error) { return displays, nil }
	d.captureDisplay = func(display desktop.Display) ([]byte, error) { return []byte(display.Name), nil }
	d.inputDisplay = func(display desktop.Display, in desktop.Input) error { input.Store(display.ID); return nil }
	return d, token, displays, input
}

func TestDesktopDisplaysListSelectionCaptureAndInput(t *testing.T) {
	d, token, displays, input := displayTestDesktop(t)
	c := testDesktopClient(t, d, token)
	ctx := context.Background()
	s, err := c.session(ctx)
	if err != nil || !s.Displays {
		t.Fatal(s, err)
	}
	listed, err := c.displays(ctx)
	if err != nil || len(listed) != 2 || listed[1].Name != displays[1].Name {
		t.Fatal(listed, err)
	}
	selected, err := c.selectDisplay(ctx, displays[1])
	if err != nil || selected != displays[1] {
		t.Fatal(selected, err)
	}
	if _, _, err := c.frame(ctx); desktopStatus(err) != 403 {
		t.Fatal("capture before start", err)
	}
	if err := c.start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.inputForDisplay(ctx, desktop.Input{Kind: "click", X: .5, Y: .5}, displays[1]); desktopStatus(err) != 409 {
		t.Fatal("input before fresh frame", err)
	}
	data, _, err := c.frame(ctx)
	if err != nil || string(data) != displays[1].Name {
		t.Fatal("wrong display captured", string(data), err)
	}
	if err := c.input(ctx, desktop.Input{Kind: "click"}); desktopStatus(err) != 409 {
		t.Fatal("untargeted input accepted after explicit selection", err)
	}
	if err := c.inputForDisplay(ctx, desktop.Input{Kind: "click"}, displays[0]); desktopStatus(err) != 409 {
		t.Fatal("old display input accepted", err)
	}
	if err := c.inputForDisplay(ctx, desktop.Input{Kind: "click", X: .3, Y: .7}, displays[1]); err != nil || input.Load() != displays[1].ID {
		t.Fatal("wrong input display", input.Load(), err)
	}
	if _, err := c.selectDisplay(ctx, displays[0]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.frame(ctx); desktopStatus(err) != 403 {
		t.Fatal("selection kept old control consent", err)
	}
	if err := c.start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.inputForDisplay(ctx, desktop.Input{Kind: "click"}, displays[0]); desktopStatus(err) != 409 {
		t.Fatal("switch did not require fresh frame", err)
	}
}

func TestDesktopDisplaysLegacyClientDefaultsToMain(t *testing.T) {
	d, token, displays, input := displayTestDesktop(t)
	if w := desktopHTTP(d, token, "POST", "/select", `{"id":0,"pid":0}`); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := desktopHTTP(d, token, "POST", "/start", `{}`); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := desktopHTTP(d, token, "GET", "/frame", ""); w.Code != 200 || w.Body.String() != displays[0].Name {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := desktopHTTP(d, token, "POST", "/input", `{"kind":"move","x":0.5,"y":0.5}`); w.Code != 204 || input.Load() != displays[0].ID {
		t.Fatal("legacy client input failed", w.Code, input.Load())
	}
}

func TestDesktopDisplaysRejectApprovalAndInvalidIdentity(t *testing.T) {
	d, token, _, _ := displayTestDesktop(t)
	for _, tc := range []struct {
		token, method, path, body string
		status                    int
	}{
		{"wrong", "GET", "/displays", "", 410}, {token, "POST", "/displays", `{}`, 405}, {token, "GET", "/display", "", 405},
		{token, "POST", "/display", `{}`, 400}, {token, "POST", "/display", `{"id":3,"uuid":"external","name":"spoof"}`, 400},
		{token, "POST", "/display", `{"id":3,"uuid":"external"}{}`, 400},
		{token, "POST", "/display", `{"id":3,"uuid":"reused-id"}`, 409}, {token, "POST", "/display", `{"id":99,"uuid":"external"}`, 409},
	} {
		if w := desktopHTTP(d, tc.token, tc.method, tc.path, tc.body); w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	d.mu.Lock()
	d.lease.pick = false
	d.mu.Unlock()
	for _, path := range []string{"/display", "/displays"} {
		method := "GET"
		if path == "/display" {
			method = "POST"
		}
		if w := desktopHTTP(d, token, method, path, `{"id":3,"uuid":"external"}`); w.Code != 403 {
			t.Fatal("approval lease selected a display", w.Code)
		}
	}
}

func TestDesktopDisplayDisconnectNeverFallsBackToMain(t *testing.T) {
	d, token, displays, input := displayTestDesktop(t)
	c := testDesktopClient(t, d, token)
	ctx := context.Background()
	if _, err := c.selectDisplay(ctx, displays[1]); err != nil {
		t.Fatal(err)
	}
	if err := c.start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.frame(ctx); err != nil {
		t.Fatal(err)
	}
	d.captureDisplay = func(display desktop.Display) ([]byte, error) { return nil, desktop.ErrDisplayUnavailable }
	d.mu.Lock()
	d.lease.lastFrame = time.Time{}
	d.mu.Unlock()
	if _, _, err := c.frame(ctx); desktopStatus(err) != 409 {
		t.Fatal(err)
	}
	if err := c.inputForDisplay(ctx, desktop.Input{Kind: "click"}, displays[1]); desktopStatus(err) != 409 || input.Load() != 0 {
		t.Fatal("disconnected display accepted input", err)
	}
	d.displays = func() ([]desktop.Display, error) { return displays[:1], nil }
	if err := c.start(ctx); desktopStatus(err) != 409 {
		t.Fatal("start silently fell back to main", err)
	}
}

func TestDesktopDisplaysRecheckLeaseAndBusyState(t *testing.T) {
	d, token, _, _ := displayTestDesktop(t)
	d.lease.unlocking = true
	if w := desktopHTTP(d, token, "POST", "/display", `{"id":3,"uuid":"external"}`); w.Code != 409 {
		t.Fatal("selection interrupted unlocking", w.Code)
	}
	d.lease.unlocking = false
	read := d.displays
	d.displays = func() ([]desktop.Display, error) { list, err := read(); d.revoke(token); return list, err }
	w := desktopHTTP(d, token, "GET", "/displays", "")
	if w.Code != 410 {
		t.Fatal("revoked lease received list", w.Code)
	}
}

func TestDesktopDisplaySessionReportsSelectedIdentity(t *testing.T) {
	d, token, displays, _ := displayTestDesktop(t)
	if w := desktopHTTP(d, token, "POST", "/display", `{"id":3,"uuid":"external"}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	w := desktopHTTP(d, token, "GET", "/session", "")
	var s desktopSession
	if json.Unmarshal(w.Body.Bytes(), &s) != nil || s.Display == nil || *s.Display != displays[1] || s.WindowOnly {
		t.Fatal(w.Body.String())
	}
}

func TestDesktopDisplaySwitchKeepsPrivacyLease(t *testing.T) {
	d, token, displays, _ := displayTestDesktop(t)
	privacy := &testDesktopPrivacy{done: make(chan struct{})}
	d.privacy = func(ctx context.Context) (desktop.Privacy, error) {
		go func() {
			select {
			case <-ctx.Done():
				_ = privacy.Close()
			case <-privacy.Done():
			}
		}()
		return privacy, nil
	}
	readyPrivacyDesktop(t, d, token)
	client := testDesktopClient(t, d, token)
	on := true
	if state, err := client.privacy(context.Background(), &on); err != nil || !state.Enabled {
		t.Fatal(state, err)
	}
	if _, err := client.selectDisplay(context.Background(), displays[1]); err != nil {
		t.Fatal(err)
	}
	if err := client.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state, err := client.privacy(context.Background(), nil); err != nil || !state.Enabled {
		t.Fatal("display switch restored physical screens", state, err)
	}
	d.revoke(token)
	select {
	case <-privacy.Done():
	case <-time.After(time.Second):
		t.Fatal("leaving desktop did not restore privacy")
	}
}
