package delegation

import "testing"

// The request of a Bot task is the name its caller wrote. The Bot that name
// meant is settled when the task is accepted, and a retry does not look it
// up again.
func TestBotTaskRetryKeepsTheAcceptedBot(t *testing.T) {
	m, _ := harness(t, 4)
	in := input("bot")
	in.Bot, in.BotID = "Rin", "b1"
	first := create(t, m, in)
	if first.Bot != "b1" || !first.NewSession {
		t.Fatalf("accepted task = Bot %q, new session %v; want b1 starting its conversation", first.Bot, first.NewSession)
	}
	for _, id := range []string{"", "b1", "b2"} {
		in.BotID = id
		again := create(t, m, in)
		if again.ID != first.ID || again.Bot != "b1" {
			t.Fatalf("retry resolving the name to %q: task %s for Bot %q, want %s for b1", id, again.ID, again.Bot, first.ID)
		}
	}
	in.Bot = "Ren"
	if _, err := m.Create(in); !IsCode(err, "REQUEST_CONFLICT") {
		t.Fatalf("the same request for another Bot: %v", err)
	}
	in.Bot = ""
	if _, err := m.Create(in); !IsCode(err, "REQUEST_CONFLICT") {
		t.Fatalf("the same request without the Bot: %v", err)
	}
}

// Each task starts a new conversation of its Bot, so a Bot takes a second task
// while the first runs. Its tasks wait for a free place like any new
// conversation.
func TestBotTakesTasksWhileItWorks(t *testing.T) {
	m, _ := harness(t, 1)
	task := func(request, name, id string) StartInput {
		in := input(request)
		in.Bot, in.BotID = name, id
		return in
	}
	first := create(t, m, task("first", "Rin", "b1"))
	second := create(t, m, task("second", "Rin", "b1"))
	if second.ID == first.ID || second.State != Queued || second.Bot != "b1" || !second.NewSession {
		t.Fatalf("a second task for the Bot = %+v, want it queued for b1 in a new conversation", second)
	}
	create(t, m, input("plain"))
}
