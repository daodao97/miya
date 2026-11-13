//go:build darwin && !ios

package desktop

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"os"
	"testing"

	dc "miya/internal/desktopcontrol"
)

// Opt in to reading real screen pixels. No image or window title is logged.
func TestCaptureNative(t *testing.T) {
	if os.Getenv("MIYA_DESKTOP_CAPTURE_PROBE") != "1" {
		t.Skip("opt-in screen capture")
	}
	locked, err := ScreenLocked()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("screen locked: %v", locked)
	ds, err := Displays()
	if err != nil || len(ds) == 0 {
		t.Fatalf("displays=%d: %v", len(ds), err)
	}
	check := func(name string, b []byte, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		c, err := jpeg.DecodeConfig(bytes.NewReader(b))
		if err != nil || c.Width <= 0 || c.Height <= 0 || c.Width > 1600 || c.Height > 1600 || len(b) > 2<<20 {
			t.Fatalf("%s: %v %v bytes=%d", name, c, err, len(b))
		}
		t.Logf("%s: %dx%d JPEG, %d bytes", name, c.Width, c.Height, len(b))
	}
	b, err := Capture()
	check("main", b, err)
	b, err = CaptureDisplay(ds[0])
	check("display", b, err)
	stale := ds[0]
	stale.UUID = "00000000-0000-0000-0000-000000000000"
	if _, err := CaptureDisplay(stale); !errors.Is(err, ErrDisplayUnavailable) {
		t.Fatalf("foreign display: %v", err)
	}
	ws, err := Windows()
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) > 0 {
		b, err = CaptureWindow(ws[0])
		check("window", b, err)
		stale := ws[0]
		stale.PID = 2147483647
		if _, err := CaptureWindow(stale); !errors.Is(err, ErrWindowUnavailable) {
			t.Fatalf("foreign PID: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := dc.CaptureDisplay(ctx, dc.Display{ID: ds[0].ID, UUID: ds[0].UUID}, dc.CaptureOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
