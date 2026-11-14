package integration

import (
	"encoding/json"
	codex "miya/internal/agent/codex/native"
	codexprojection "miya/internal/agent/codex/projection"
	msgfmt "miya/internal/chat/presentation"
	"testing"
)

func TestNSGoHistoryRestoresWorkedDuration(t *testing.T) {
	var remote codex.Thread
	if err := json.Unmarshal([]byte(`{"turns":[{"id":"turn1","status":"completed","startedAt":1700000000,"completedAt":1700000126,"durationMs":125500,"items":[{"id":"u","type":"userMessage","content":[{"type":"text","text":"question"}]},{"id":"a","type":"agentMessage","text":"Done"}]}]}`), &remote); err != nil {
		t.Fatal(err)
	}
	messages := codexprojection.HistoryMessages(remote)
	groups := msgfmt.GroupMessages(messages)
	if len(groups) != 2 || msgfmt.TurnElapsedLabel(groups[1].Messages, false) != "Worked for 2m 5s" {
		t.Fatalf("history lost duration: %+v", messages)
	}
	// Repeated history reads must not turn a fixed duration into time since now.
	if got := msgfmt.TurnElapsedLabel(codexprojection.HistoryMessages(remote)[1:], false); got != "Worked for 2m 5s" {
		t.Fatal(got)
	}
}
