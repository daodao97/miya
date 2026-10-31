package main

import "testing"

// A Codex user message with a turn made the phone's rewind check call a nil
// ready signal, ending the app as a Codex conversation opened.
func TestViewerControllerCodexRewindCheck(t *testing.T) {
	c := newViewerController()
	th := Thread{ID: 1, ServerID: "thread", Agent: agentCodex}
	m := Message{ID: 1, Role: "user", TurnID: "turn"}
	if c.canRewind(th, m) {
		t.Fatal("the phone's viewer should not offer a rewind")
	}
}
