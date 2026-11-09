package presentation

import (
	"testing"

	"miya/internal/chat/model"
)

func TestReasoningStatusIsNotSummaryContent(t *testing.T) {
	for _, content := range []string{"", " \n", "Thinking…", " Thinking...\n", "思考中…", "思考中..."} {
		for _, explicit := range []bool{false, true} {
			m := model.Message{Role: "activity", Kind: "reasoning", Status: "completed", Content: content}
			if explicit {
				m.Presentation = &model.MessagePresentation{Content: content, Title: "Thought", Format: "markdown"}
			}
			if got := Message(m); got.Content != "" || got.Title == "" {
				t.Fatalf("status placeholder became a summary: content=%q explicit=%v got=%+v", content, explicit, got)
			}
		}
	}
	for _, content := range []string{"Thinking... about the next step.", "**Check the input**\nThen validate the result."} {
		if got := Message(model.Message{Kind: "reasoning", Content: content}); got.Content != content {
			t.Fatalf("real summary changed: %q", got.Content)
		}
	}
	if got := Message(model.Message{Role: "assistant", Content: "Thinking..."}); got.Content != "Thinking..." {
		t.Fatal("reasoning cleanup changed an assistant response")
	}
}
