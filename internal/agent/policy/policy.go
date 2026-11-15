package policy

// Read and Edit are set only by automations that declare an access level
// (docs/action-review.md); the composer and settings offer the other three.
// Normalize keeps them so later messages of such a run keep its level.
const (
	Read = "read"
	Edit = "edit"
)

func Normalize(policy string) string {
	switch policy {
	case "on-request", "on-failure", "never", Read, Edit:
		return policy
	default:
		return "on-request"
	}
}

// Unattended reports whether policy belongs to an automation's access level,
// whose approval requests Miya answers itself instead of waiting for a person.
func Unattended(policy string) bool { return policy == Read || policy == Edit }
