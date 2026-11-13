//go:build darwin && !ios

package desktop

import (
	"context"
	"errors"
	"testing"

	dc "miya/internal/desktopcontrol"
)

func TestNativeInputCoordinates(t *testing.T) {
	b := dc.Rect{X: -1920, Y: 100, Width: 1920, Height: 1080}
	in := inputAtBounds(Input{Kind: "drag", X: 0, Y: 1, ToX: 1, ToY: 0}, b)
	if in.Kind != dc.Drag || in.X != -1920 || in.Y != 1179 || in.ToX != -1 || in.ToY != 100 {
		t.Fatalf("wrong desktop points: %+v", in)
	}
	key := inputAtBounds(Input{Kind: "key", Key: "a", Modifier: "command"}, b)
	if key.Kind != dc.Key || key.KeyCode != 0 || key.Modifiers != dc.Command {
		t.Fatalf("wrong key: %+v", key)
	}
}

func TestNativeAdapterErrors(t *testing.T) {
	for _, tc := range []struct{ native, want error }{
		{dc.ErrWindowUnavailable, ErrWindowUnavailable}, {dc.ErrDisplayUnavailable, ErrDisplayUnavailable},
	} {
		if !errors.Is(desktopError(tc.native), tc.want) {
			t.Fatal(tc.native)
		}
	}
	if desktopError(context.DeadlineExceeded).Error() != permissionError(8).Error() {
		t.Fatal("lost capture timeout")
	}
	if desktopError(dc.ErrScreenRecording).Error() != permissionError(1).Error() {
		t.Fatal("lost recording permission")
	}
	if err := SendWindowInput(Window{}, Input{Kind: "text", Text: "never sent"}); !errors.Is(err, ErrWindowUnavailable) {
		t.Fatal(err)
	}
	if _, err := CaptureDisplay(Display{}); !errors.Is(err, ErrDisplayUnavailable) {
		t.Fatal(err)
	}
}
