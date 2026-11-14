package presentation

import (
	"testing"

	"miya/internal/chat/model"
)

func TestGroupMessagesSkipsEmptyFinishedThoughts(t *testing.T) {
	messages := []model.Message{
		{ID: 1, Role: "user", Content: "question"},
		{ID: 2, Role: "activity", Kind: "reasoning", Status: "completed"},
		{ID: 3, Role: "activity", Kind: "reasoning", Status: "completed", Content: "Thinking…"},
		{ID: 4, Role: "activity", Kind: "reasoning", Status: "completed", Content: "Weighing two layouts"},
		{ID: 5, Role: "activity", Kind: "reasoning", Status: "inProgress"},
		{ID: 6, Role: "assistant", Kind: "agentMessage", Content: "answer"},
	}
	groups := GroupMessages(messages)
	if len(groups) != 2 {
		t.Fatalf("groups = %d", len(groups))
	}
	var ids []int
	for _, m := range groups[1].Messages {
		ids = append(ids, m.ID)
	}
	if len(ids) != 3 || ids[0] != 4 || ids[1] != 5 || ids[2] != 6 {
		t.Fatalf("response shows %v, want the thought with text, the one in progress and the answer", ids)
	}
}
