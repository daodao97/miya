package main

import (
	"path/filepath"
	"testing"

	"miya/internal/desktop"
)

func TestDesktopInputAcceptsCursorMove(t *testing.T) {
	if err := (desktop.Input{Kind: "move", X: .5, Y: .5}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (desktop.Input{Kind: "move", X: 1.5}).Validate(); err == nil {
		t.Fatal("out-of-range move accepted")
	}
}

func TestDesktopPrefsRememberPointerModeOnPhone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs", "desktop-control.json")
	if readDesktopPrefs(path).PointerMode != "trackpad" {
		t.Fatal("default pointer mode")
	}
	if err := writeDesktopPrefs(path, "touch"); err != nil || readDesktopPrefs(path).PointerMode != "touch" {
		t.Fatal("touch mode not remembered", err)
	}
	if err := writeDesktopPrefs(path, "bogus"); err != nil || readDesktopPrefs(path).PointerMode != "touch" {
		t.Fatal("invalid mode overwrote the preference", err)
	}
}
