package policy

func Normalize(policy string) string {
	switch policy {
	case "on-request", "on-failure", "never":
		return policy
	default:
		return "on-request"
	}
}
