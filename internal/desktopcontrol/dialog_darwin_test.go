//go:build darwin && !ios

package desktopcontrol

import (
	"errors"
	"testing"
)

func TestDialogRejectsInvalidWindow(t *testing.T) {
	if _, err := ReadDialog(Window{}); !errors.Is(err, ErrWindowUnavailable) {
		t.Errorf("ReadDialog(zero window) = %v", err)
	}
	if err := Press(Window{ID: 1, PID: 1}, ""); !errors.Is(err, ErrWindowUnavailable) {
		t.Errorf("Press(empty label) = %v", err)
	}
}

// Read only: it presses nothing, and logs counts, not contents.
func TestReadDialog(t *testing.T) {
	ws, err := Windows()
	if err != nil || len(ws) == 0 {
		t.Skip("no windows:", err)
	}
	for _, w := range ws {
		if w.Layer != 0 {
			continue
		}
		d, err := ReadDialog(w)
		if errors.Is(err, ErrAccessibility) {
			t.Skip(err)
		}
		if errors.Is(err, ErrWindowUnavailable) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Actions) > dialogButtons {
			t.Errorf("%d actions", len(d.Actions))
		}
		t.Logf("%s: title=%dB message=%dB actions=%d", w.App, len(d.Title), len(d.Message), len(d.Actions))
		return
	}
	t.Skip("no readable window")
}
