package main

import (
	"context"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"miya/internal/desktop"
)

func testDesktopClient(t *testing.T, d *desktopControl, token string) *desktopClient {
	t.Helper()
	server := httptest.NewServer(d)
	t.Cleanup(server.Close)
	addr := server.Listener.Addr().String()
	return newDesktopClient(func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}, token)
}

func TestDesktopClientConsentCaptureInputAndStop(t *testing.T) {
	d, token, calls := mockDesktop(t)
	c := testDesktopClient(t, d, token)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := c.session(ctx)
	if err != nil || session.WindowOnly || session.Window != nil {
		t.Fatal(session, err)
	}
	if _, _, err := c.frame(ctx); desktopStatus(err) != 403 {
		t.Fatal("captured before consent", err)
	}
	if err := c.start(ctx); err != nil {
		t.Fatal(err)
	}
	data, _, err := c.frame(ctx)
	if err != nil || string(data) != "jpeg" {
		t.Fatal(string(data), err)
	}
	if err := c.input(ctx, desktop.Input{Kind: "click", X: .3, Y: .5}); err != nil || calls.Load() != 1 {
		t.Fatal("input did not reach the Mac", err, calls.Load())
	}
	if err := c.input(ctx, desktop.Input{Kind: "click", X: 1.5}); err == nil || calls.Load() != 1 {
		t.Fatal("invalid input was sent")
	}
	c.stop()
	if _, _, err := c.frame(ctx); desktopStatus(err) != 410 {
		t.Fatal("frame after stop", err)
	}
}

func TestDesktopClientRejectsWrongToken(t *testing.T) {
	d, _, _ := mockDesktop(t)
	c := testDesktopClient(t, d, "wrong")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.session(ctx); desktopStatus(err) != 410 {
		t.Fatal("wrong token accepted", err)
	}
}
