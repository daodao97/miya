package appmodel

import (
	model "miya/internal/chat/model"
)

func AgentName(id string) string { return model.AgentName(id) }

func SameMessageOrder(messages []model.Message, order []int) bool {
	return model.SameMessageOrder(messages, order)
}
