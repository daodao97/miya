package widgets

import (
	appmodel "miya/internal/appmodel"
	codex "miya/internal/codex"
	strings "strings"
)

func RequestAgent(a codex.Approval) string {
	if appmodel.IsMiyaApproval(a) {
		return "Miya"
	}
	if strings.Contains(string(a.ID), "claude:") {
		return appmodel.AgentName(appmodel.AgentClaude)
	}
	return appmodel.AgentName(appmodel.AgentCodex)
}
