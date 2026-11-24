package manager

import (
	"testing"

	"miya/internal/chat/model"
)

// A Bot's charter goes with every message to its conversations, also where
// memory does not: with memory off, for delegated tasks and for automations.
// A command goes to the agent as typed.
func TestInputCarriesTheCharter(t *testing.T) {
	mg := &Manager{}
	for name, tc := range map[string]struct {
		thread model.Thread
		req    model.SendRequest
	}{
		"memory off": {model.Thread{MemoryDisabled: true}, model.SendRequest{Text: "hi", Charter: "\n\nC"}},
		"delegated":  {model.Thread{}, model.SendRequest{Text: "hi", Charter: "\n\nC", MemoryControl: true}},
		"automation": {model.Thread{Automation: "a1"}, model.SendRequest{Text: "hi", Charter: "\n\nC"}},
		"no store":   {model.Thread{}, model.SendRequest{Text: "hi", Charter: "\n\nC"}},
	} {
		in := mg.PrepareInput(tc.thread, tc.req, "/work/app", "s")
		if got := in.Text("hi"); got != "hi\n\nC" {
			t.Errorf("%s: %q, want the charter after the message", name, got)
		}
		in.Finish(true)
	}
	in := mg.PrepareInput(model.Thread{}, model.SendRequest{Text: "/goal ship it", Charter: "\n\nC"}, "/work/app", "s")
	if got := in.Text("/goal ship it"); got != "/goal ship it" {
		t.Errorf("a command became %q", got)
	}
	if got := mg.PrepareInput(model.Thread{}, model.SendRequest{Text: "hi"}, "/work/app", "s").Text("hi"); got != "hi" {
		t.Errorf("a message outside a Bot's conversation became %q", got)
	}
}
