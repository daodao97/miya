package desktop

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"time"

	"github.com/egoist/mygo/plugins/permissionflow"
)

const PermissionStatusOp = "desktop-permissions"
const AccessibilityCheckArg = "-desktop-accessibility-check"

type Permissions struct {
	Supported       bool                  `json:"supported"`
	ScreenRecording permissionflow.Status `json:"screenRecording"`
	Accessibility   permissionflow.Status `json:"accessibility"`
}

// CheckPermissions reads the current process's permissions without requesting them.
func CheckPermissions() Permissions {
	if !Available() {
		return Permissions{}
	}
	return Permissions{
		Supported:       true,
		ScreenRecording: checkPermission(permissionflow.ScreenRecording),
		Accessibility:   CheckAccessibility(),
	}
}

func CheckAccessibility() permissionflow.Status {
	return checkPermission(permissionflow.Accessibility)
}

// CheckAccessibilityGrant reads the system grant in a fresh instance of Miya.
// macOS can cache AXIsProcessTrusted in both the window and service processes.
func CheckAccessibilityGrant() permissionflow.Status {
	exe, err := os.Executable()
	if err != nil {
		return permissionflow.Unknown
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, AccessibilityCheckArg).Output()
	var status permissionflow.Status
	if err != nil || json.Unmarshal(out, &status) != nil {
		return permissionflow.Unknown
	}
	return status
}

func checkPermission(p permissionflow.Permission) permissionflow.Status {
	status, err := permissionflow.Check(p)
	if err != nil {
		return permissionflow.Unknown
	}
	return status
}
