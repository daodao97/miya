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

// A Bot has one standing conversation, so one task at a time may start it.
func TestOneTaskStartsABotsConversation(t *testing.T) {
	m, _ := harness(t, 1)
	task := func(request, name, id string) StartInput {
		in := input(request)
		in.Bot, in.BotID = name, id
		return in
	}
	create(t, m, task("first", "Rin", "b1"))
	if _, err := m.Create(task("second", "Rin", "b1")); !IsCode(err, "THREAD_BUSY") {
		t.Fatalf("a second task starting the conversation: %v", err)
	}

	// Another Bot's conversation waits for a free place like any new one,
	// and holds its Bot while it waits.
	queued := create(t, m, task("other", "Ren", "b2"))
	if queued.State != Queued || queued.Bot != "b2" {
		t.Fatalf("another Bot's task = %+v, want it queued for b2", queued)
	}
	if _, err := m.Create(task("other-again", "Ren", "b2")); !IsCode(err, "THREAD_BUSY") {
		t.Fatalf("a second task behind a queued one: %v", err)
	}

	// Once that task is over without a conversation, another may start it.
	if q, err := m.Cancel(queued.From, queued.ID); err != nil || !Terminal(q.State) {
		t.Fatalf("cancel the queued task: %+v %v", q, err)
	}
	if next := create(t, m, task("other-next", "Ren", "b2")); next.Bot != "b2" {
		t.Fatalf("task after the cancelled one = %+v", next)
	}

	// Tasks that name no Bot are not held back.
	create(t, m, input("plain"))
}
