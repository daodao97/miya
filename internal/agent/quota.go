package agent

import (
	claudequota "miya/internal/agent/claude/quota"
	codexquota "miya/internal/agent/codex/quota"
	"miya/internal/agent/quota"
)

// Quotas composes the built-in readers. The service and its consumers only
// depend on quota.Provider; native parsing belongs to each integration.
func Quotas() *quota.Service {
	return quota.NewService(codexquota.Provider{}, claudequota.Provider{})
}
