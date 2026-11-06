package integration

import (
	"testing"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
)

// A Codex user message with a turn made the phone's rewind check call a nil
// ready signal, ending the app as a Codex conversation opened.
func TestViewerControllerCodexRewindCheck(t *testing.T) {
	c := newViewerController()
	th := model.Thread{ID: 1, ServerID: "thread", Agent: appmodel.AgentCodex}
	m := model.Message{ID: 1, Role: "user", TurnID: "turn"}
	if c.CanRewind(th, m) {
		t.Fatal("the phone's viewer should not offer a rewind")
	}
}
