package controller

import (
	"context"
	"testing"

	pinative "miya/internal/agent/pi/native"
	"miya/internal/chat/model"
)

// A phone opening a pi conversation reads Miya's record of it. Codex never
// knew the conversation, so asking Codex fails with "thread not loaded".
func TestReadOnlyHistoryReadsPiRecord(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	const id = "01a1b812-09a6-70a7-9380-69b8c8419502"
	pinative.SaveRecord(model.Thread{ServerID: id, Agent: "pi", Messages: []model.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi", Status: "inProgress"},
	}}, "")

	messages, status := readOnlyHistory(context.Background(), phoneThread{Thread: model.Thread{ServerID: id, Agent: "pi"}, Found: true})
	if status != "" {
		t.Fatalf("status = %q", status)
	}
	if len(messages) != 2 || messages[0].Content != "hello" || messages[1].Content != "hi" {
		t.Fatalf("messages = %+v", messages)
	}
	if messages[0].ID != 1 || messages[1].ID != 2 {
		t.Fatalf("ids = %d, %d", messages[0].ID, messages[1].ID)
	}
	if messages[1].Status != "interrupted" {
		t.Fatalf("unfinished reply status = %q", messages[1].Status)
	}
}

func TestReadOnlyHistoryReportsMissingPiRecord(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	_, status := readOnlyHistory(context.Background(), phoneThread{Thread: model.Thread{ServerID: "missing", Agent: "pi"}, Found: true})
	if status == "" || status == "Desktop is connecting to Codex…" {
		t.Fatalf("status = %q", status)
	}
}
