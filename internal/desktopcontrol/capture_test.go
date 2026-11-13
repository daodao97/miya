package desktopcontrol

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestCaptureValidation(t *testing.T) {
	for _, o := range []CaptureOptions{{MaxDimension: -1}, {MaxDimension: 16385}, {Quality: math.NaN()}, {Quality: math.Inf(1)}, {Quality: -0.1}, {Quality: 1.1}} {
		if _, err := CaptureDisplay(context.Background(), Display{ID: 1, UUID: "no-native-call"}, o); err == nil {
			t.Fatalf("accepted %+v", o)
		}
	}
	if _, err := CaptureDisplay(context.Background(), Display{}, CaptureOptions{}); !errors.Is(err, ErrDisplayUnavailable) {
		t.Fatal(err)
	}
	if _, err := CaptureWindow(context.Background(), Window{}, CaptureOptions{}); !errors.Is(err, ErrWindowUnavailable) {
		t.Fatal(err)
	}
}
