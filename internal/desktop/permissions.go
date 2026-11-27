package desktop

import "miya/internal/desktopcontrol"

const PermissionStatusOp = "desktop-permissions"

// PermissionStatus preserves the values sent by older desktop services.
type PermissionStatus int

const (
	PermissionUnknown PermissionStatus = 0
	PermissionDenied  PermissionStatus = 2
	PermissionGranted PermissionStatus = 3
)

type Permissions struct {
	Supported       bool             `json:"supported"`
	ScreenRecording PermissionStatus `json:"screenRecording"`
	Accessibility   PermissionStatus `json:"accessibility"`
}

func CheckPermissions() Permissions {
	if !Available() {
		return Permissions{}
	}
	return Permissions{
		Supported:       true,
		ScreenRecording: permissionStatus(desktopcontrol.ScreenRecordingAllowed()),
		Accessibility:   permissionStatus(desktopcontrol.AccessibilityAllowed()),
	}
}

func permissionStatus(allowed bool, err error) PermissionStatus {
	if err != nil {
		return PermissionUnknown
	}
	if allowed {
		return PermissionGranted
	}
	return PermissionDenied
}
