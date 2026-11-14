//go:build !windows

package integration

import (
	claude "miya/internal/agent/claude/native"
	"testing"
)

func TestClaudePermissionModes(t *testing.T) {
	for policy, want := range map[string]string{"on-request": "manual", "on-failure": "auto", "never": "bypassPermissions", "": "manual"} {
		if got := claude.PermissionMode(policy); got != want {
			t.Errorf("%q → %q, want %q", policy, got, want)
		}
	}
}
