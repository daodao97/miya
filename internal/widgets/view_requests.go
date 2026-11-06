package widgets

import (
	"strings"

	"miya/internal/appmodel"
	"miya/internal/codex"
)

func RequestAgent(a codex.Approval) string {
	if codex.IsMiyaApproval(a) {
		return "Miya"
	}
	if strings.Contains(string(a.ID), "claude:") {
		return appmodel.AgentName(appmodel.AgentClaude)
	}
	return appmodel.AgentName(appmodel.AgentCodex)
}
