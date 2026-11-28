package desktopcontrol

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"time"
)

const AccessibilityCheckArg = "-desktop-accessibility-check"

// FreshAccessibilityGrant reads the grant in a new process because macOS
// can cache AXIsProcessTrusted in the running window and service.
func FreshAccessibilityGrant() (bool, error) {
	exe, err := os.Executable()
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, AccessibilityCheckArg).Output()
	if err != nil {
		return false, err
	}
	var granted bool
	err = json.Unmarshal(out, &granted)
	return granted, err
}
