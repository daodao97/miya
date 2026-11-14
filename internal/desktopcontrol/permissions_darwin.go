//go:build darwin && !ios

package desktopcontrol

import (
	"fmt"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// The two permissions remote desktop needs: Screen Recording to capture and
// Accessibility to send input. Checking never prompts; requesting shows the
// system's prompt, or nothing once the user answered, and returns at once.

var (
	permOnce sync.Once
	permErr  error

	permScreenPreflight func() bool
	permScreenRequest   func() bool
	permAXTrusted       func() bool
	permAXTrustedWith   func(options objc.ID) bool
	permAXPromptKey     objc.ID
)

func loadPermissions() error {
	permOnce.Do(func() {
		cg, err := purego.Dlopen(coreGraphicsPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			permErr = fmt.Errorf("desktopcontrol: %w", err)
			return
		}
		as, err := purego.Dlopen(applicationServicesPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			permErr = fmt.Errorf("desktopcontrol: %w", err)
			return
		}
		for _, f := range []struct {
			fn   any
			lib  uintptr
			name string
		}{
			{&permScreenPreflight, cg, "CGPreflightScreenCaptureAccess"},
			{&permScreenRequest, cg, "CGRequestScreenCaptureAccess"},
			{&permAXTrusted, as, "AXIsProcessTrusted"},
			{&permAXTrustedWith, as, "AXIsProcessTrustedWithOptions"},
		} {
			p, err := purego.Dlsym(f.lib, f.name)
			if err != nil || p == 0 {
				permErr = fmt.Errorf("desktopcontrol: missing %s: %w", f.name, ErrUnsupported)
				return
			}
			purego.RegisterFunc(f.fn, p)
		}
		permAXPromptKey, permErr = constID(as, "kAXTrustedCheckOptionPrompt")
	})
	return permErr
}

// ScreenRecordingAllowed reports whether the app may capture the screen.
func ScreenRecordingAllowed() (bool, error) {
	if err := loadPermissions(); err != nil {
		return false, err
	}
	return permScreenPreflight(), nil
}

// RequestScreenRecording asks for Screen Recording: the system prompts the
// first time, and the app shows in the list of System Settings after.
func RequestScreenRecording() bool {
	if loadPermissions() != nil {
		return false
	}
	return permScreenPreflight() || permScreenRequest()
}

// AccessibilityAllowed reports whether the app may send input to others.
func AccessibilityAllowed() (bool, error) {
	if err := loadPermissions(); err != nil {
		return false, err
	}
	return permAXTrusted(), nil
}

// RequestAccessibility asks for Accessibility with the system's prompt.
func RequestAccessibility() bool {
	if loadPermissions() != nil {
		return false
	}
	if permAXTrusted() {
		return true
	}
	yes := objc.ID(objc.GetClass("NSNumber")).Send(objc.RegisterName("numberWithBool:"), true)
	opts := objc.ID(objc.GetClass("NSDictionary")).Send(objc.RegisterName("dictionaryWithObject:forKey:"), yes, permAXPromptKey)
	return permAXTrustedWith(opts)
}
