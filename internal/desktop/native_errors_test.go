package desktop

import (
	"errors"
	"strings"
	"testing"
)

func TestNativeCaptureFailuresDoNotClaimUnsupportedOS(t *testing.T) {
	for _, code := range []int{5, 6, 7, 8, 9, 10, 11, 999} {
		err := permissionError(code)
		if err == nil || strings.Contains(err.Error(), "macOS 14") || strings.Contains(err.Error(), "Screen Recording") {
			t.Fatalf("%d: misleading error %v", code, err)
		}
	}
	if !strings.Contains(permissionError(3).Error(), "macOS 14") {
		t.Fatal("unsupported OS missing version requirement")
	}
	if !errors.Is(permissionError(4), ErrWindowUnavailable) {
		t.Fatal("lost target identity error")
	}
	err := captureError(7, "com.apple.ScreenCaptureKit.SCStreamErrorDomain (-3811)")
	if err == nil || !strings.Contains(err.Error(), "-3811") || !strings.Contains(err.Error(), "Unable to capture") {
		t.Fatal("native diagnostic discarded", err)
	}
	if captureError(0, "") != nil {
		t.Fatal("successful capture failed")
	}
}
