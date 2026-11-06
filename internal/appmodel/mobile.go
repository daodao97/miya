package appmodel

import (
	model "miya/internal/model"
)

func AgentName(agent string) string {
	if agent == AgentClaude {
		return "Claude Code"
	}
	return "Codex"
}

func SameMessageOrder(messages []model.Message, order []int) bool {
	if len(messages) != len(order) {
		return false
	}
	for i, m := range messages {
		if m.ID != order[i] {
			return false
		}
	}
	return true
}
