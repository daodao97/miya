package main

import (
	"encoding/json"
	"miya/internal/codex"
	"testing"
)

func TestHistoryPreservesTurnError(t *testing.T) {
	var remote codex.Thread
	if err := json.Unmarshal([]byte(`{"turns":[{"id":"failed-turn","status":"failed","items":[],"error":{"message":"Selected model is at capacity."}}]}`), &remote); err != nil {
		t.Fatal(err)
	}
	c := &chatController{}
	messages := c.historyMessages(remote)
	if len(messages) != 1 || messages[0].Role != "error" || messages[0].Content != "Selected model is at capacity." {
		t.Fatalf("lost error: %+v", messages)
	}
}

func TestUserImageIsSeparateFromBubble(t *testing.T) {
	m, ok := itemMessage(codex.Item{Type: "userMessage", Content: json.RawMessage(`[{"type":"text","text":"question [Image: image]"},{"type":"image","url":"data:image/png;base64,AAAA"}]`)})
	if !ok || m.Content != "question" || len(m.Images) != 1 {
		t.Fatalf("incorrect image rendering data: %+v", m)
	}
}

func TestCommandActionUsesFileLabel(t *testing.T) {
	var item codex.Item
	if err := json.Unmarshal([]byte(`{"type":"commandExecution","command":"sed -n '1,180p' registration.go","commandActions":[{"type":"read","name":"registration.go"}]}`), &item); err != nil {
		t.Fatal(err)
	}
	if title := activityTitle(Message{Kind: item.Type, Item: &item}); title != "已读取 registration.go" {
		t.Fatal(title)
	}
}
