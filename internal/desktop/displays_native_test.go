//go:build darwin && !ios && cgo

package desktop

import (
	"os"
	"testing"
)

func TestDisplaysNative(t *testing.T) {
	if os.Getenv("MIYA_DESKTOP_DISPLAYS_PROBE") != "1" {
		t.Skip("opt-in installed display metadata probe")
	}
	displays, err := Displays()
	if err != nil || len(displays) == 0 {
		t.Fatal(displays, err)
	}
	main := 0
	seen := map[uint32]bool{}
	for _, display := range displays {
		if display.ID == 0 || display.UUID == "" || display.Name == "" || display.Width <= 0 || display.Height <= 0 || seen[display.ID] {
			t.Fatal("invalid display metadata", display)
		}
		seen[display.ID] = true
		if display.Main {
			main++
		}
		t.Logf("display: %s, %dx%d, main=%v", display.Name, display.Width, display.Height, display.Main)
	}
	if main != 1 || !displays[0].Main {
		t.Fatal("main display must come first", displays)
	}
}
