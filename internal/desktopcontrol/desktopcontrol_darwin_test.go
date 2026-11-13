//go:build darwin && !ios

package desktopcontrol

import (
	"errors"
	"testing"
)

func TestApplicationInfo(t *testing.T) {
	const path = "/System/Applications/Calculator.app"
	app, err := ApplicationInfo(path, 48)
	if err != nil {
		t.Fatal(err)
	}
	if app.Name == "" || app.BundleID != "com.apple.calculator" {
		t.Errorf("metadata: %+v", app)
	}
	if len(app.Icon) < 8 || string(app.Icon[1:4]) != "PNG" {
		t.Errorf("icon is not a PNG (%d bytes)", len(app.Icon))
	}
	t.Logf("%s %s icon=%dB", app.Name, app.BundleID, len(app.Icon))
	if _, err := ApplicationInfo("relative/Calculator.app", 0); err == nil {
		t.Error("relative path accepted")
	}
	if _, err := ApplicationInfo("/System/Applications/Nope.app", 0); err == nil {
		t.Error("missing bundle accepted")
	}
}

func TestSession(t *testing.T) {
	if !Available() {
		t.Skip("before macOS 14")
	}
	locked, err := Locked()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("locked: %v", locked)
}

func TestWindows(t *testing.T) {
	ws, err := Windows()
	if errors.Is(err, ErrScreenRecording) || errors.Is(err, ErrUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	seen := map[[2]uint64]bool{}
	for _, w := range ws {
		key := [2]uint64{uint64(w.ID), uint64(w.PID)}
		if w.ID == 0 || w.PID <= 0 || w.App == "" || seen[key] || w.Layer < 0 {
			t.Errorf("invalid window: %+v", w)
		}
		seen[key] = true
	}
	t.Logf("%d windows", len(ws))
}

func TestDisplays(t *testing.T) {
	ds, err := Displays()
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) == 0 {
		t.Skip("no active display")
	}
	if !ds[0].Main {
		t.Errorf("first display is not the main one: %+v", ds[0])
	}
	for _, d := range ds {
		if d.ID == 0 || d.UUID == "" || d.PixelWidth <= 0 || d.PixelHeight <= 0 || d.Bounds.Width <= 0 || d.Bounds.Height <= 0 {
			t.Errorf("incomplete display: %+v", d)
		}
		if !displayMatches(d.ID, d.UUID) {
			t.Errorf("display %d does not match its own UUID %s", d.ID, d.UUID)
		}
		if displayMatches(d.ID, "00000000-0000-0000-0000-000000000000") {
			t.Errorf("display %d matches a foreign UUID", d.ID)
		}
		t.Logf("%+v", d)
	}
}
