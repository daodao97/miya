package prefs

// "on-failure" is retained as the stored ID for the existing Help me approve
// preference; it must not be sent as the protocol's deprecated failure policy.
func ApprovalForPolicy(policy string) string {
	if NormalizePermissionPolicy(policy) == "never" {
		return "never"
	}
	return "on-request"
}

func SandboxForPolicy(policy string) string {
	if policy == "never" {
		return "danger-full-access"
	}
	return "workspace-write"
}

func ReviewerForPolicy(policy string) string {
	if policy == "on-failure" {
		return "auto_review"
	}
	return "user"
}

func TurnSandboxForPolicy(policy string) map[string]any {
	if policy == "never" {
		return map[string]any{"type": "dangerFullAccess"}
	}
	return map[string]any{"type": "workspaceWrite", "networkAccess": false}
}
