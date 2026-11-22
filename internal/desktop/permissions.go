package desktop

import "github.com/egoist/mygo/plugins/permissionflow"

const PermissionStatusOp = "desktop-permissions"

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
		Accessibility:   checkPermission(permissionflow.Accessibility),
	}
}

func checkPermission(p permissionflow.Permission) permissionflow.Status {
	status, err := permissionflow.Check(p)
	if err != nil {
		return permissionflow.Unknown
	}
	return status
}
