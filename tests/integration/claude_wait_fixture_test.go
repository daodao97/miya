package integration

import (
	"miya/internal/controller"
)

func waitClaudeHistory(c *controller.Controller) {
	if c.ClaudeHistoryDone != nil {
		<-c.ClaudeHistoryDone
	}
}

func backendReadyFor(c *controller.Controller, agent string) bool {
	return agent == "claude-code" && c.ClaudeBinary != ""
}
