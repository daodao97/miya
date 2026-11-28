package desktopui

import (
	"testing"

	"github.com/egoist/mygo/plugins/permissionflow"

	"miya/internal/desktop"
)

func TestRemoteControlAccessibilityStatus(t *testing.T) {
	for _, tc := range []struct {
		name                string
		active, grant, want permissionflow.Status
		restartRequired     bool
	}{
		{"grant pending in the service", permissionflow.Denied, permissionflow.Granted, permissionflow.Granted, true},
		{"grant applied", permissionflow.Granted, permissionflow.Granted, permissionflow.Granted, false},
		{"revoked with a stale service result", permissionflow.Granted, permissionflow.Denied, permissionflow.Denied, false},
		{"system check unavailable", permissionflow.Denied, permissionflow.Unknown, permissionflow.Denied, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := remoteControlUI{
				permissions:        &desktop.Permissions{Accessibility: tc.active},
				accessibilityGrant: tc.grant,
			}
			status, restartRequired := s.accessibilityStatus()
			if status != tc.want || restartRequired != tc.restartRequired {
				t.Fatalf("got status %s, restart required %t; want %s, %t", status, restartRequired, tc.want, tc.restartRequired)
			}
		})
	}
}
