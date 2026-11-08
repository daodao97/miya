package client

import (
	"testing"

	"miya/internal/chat/model"
)

func TestMirrorResetDropsCacheOnlyForNewSource(t *testing.T) {
	m := Mirror{Drafts: map[int]string{1: "Draft"}, Messages: map[int][]model.Message{1: {{Content: "Answer"}}}}
	m.ResetForSource(true)
	if m.Drafts[1] != "Draft" || len(m.Messages[1]) != 1 {
		t.Fatal("same source lost cached content")
	}
	m.ResetForSource(false)
	if len(m.Drafts) != 0 || len(m.Messages) != 0 {
		t.Fatal("cached content leaked between service sources")
	}
}
