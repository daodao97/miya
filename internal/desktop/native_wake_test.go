package desktop

import (
	"os"
	"testing"
)

// Opt-in: lights the real display. The caller checks display state around it.
func TestWakeDisplayNative(t *testing.T) {
	if os.Getenv("MIYA_DESKTOP_WAKE_PROBE") != "1" {
		t.Skip("opt-in native display wake probe")
	}
	if err := WakeDisplay(); err != nil {
		t.Fatal(err)
	}
}
