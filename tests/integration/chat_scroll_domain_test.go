package integration

import (
	"fmt"
	"miya/internal/chat/model"
)

func scrollTestMessages() []model.Message {
	var messages []model.Message
	for i := 0; i < 40; i++ {
		messages = append(messages, model.Message{ID: i + 100, Role: "user", Content: fmt.Sprintf("history %02d", i)})
	}
	return messages
}
