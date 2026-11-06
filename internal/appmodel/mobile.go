package appmodel

import (
	model "miya/internal/chat/model"
)

func AgentName(id string) string { return model.AgentName(id) }

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
