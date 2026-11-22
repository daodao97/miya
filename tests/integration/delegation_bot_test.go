//go:build !windows

package integration

import (
	"strings"
	"testing"
	"time"

	"miya/internal/appmodel"
	"miya/internal/bot"
	"miya/internal/chat/model"
)

// An agent hands a task to a Bot by the name list_agents gave. The first task
// starts the Bot's standing conversation, with the Bot's introduction, and
// the next one goes to that same conversation.
func TestCollaborationTaskGoesToABotsStandingConversation(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, appmodel.AgentClaude, "initiator")
	var rin bot.Bot
	f.serv.Do(func() {
		var err error
		if rin, err = f.C.BotOp("botSave", bot.Request{Bot: bot.Bot{Name: "Rin", Note: "Reviews pull requests", Agent: appmodel.AgentClaude, Project: f.C.Cwd}}); err != nil {
			t.Errorf("save the Bot: %v", err)
		}
	})
	if rin.ID == "" {
		t.Fatal("the Bot was not saved")
	}

	// conversation is the state list_agents gives for the Bot's conversation.
	conversation := func() string {
		t.Helper()
		listed, err := collaborationMCPCall(t, f, caller, "list_agents", map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		bots, _ := listed["bots"].([]any)
		if len(bots) != 1 {
			t.Fatalf("list_agents lists the Bots %v, want Rin alone", listed["bots"])
		}
		entry, _ := bots[0].(map[string]any)
		if entry["name"] != "Rin" || entry["role"] != "Reviews pull requests" || entry["agent"] != string(appmodel.AgentClaude) {
			t.Fatalf("list_agents shows the Bot as %v", entry)
		}
		state, _ := entry["conversation"].(string)
		return state
	}
	// settled waits until the conversation at ref can take another task.
	settled := func(ref string) model.Thread {
		t.Helper()
		var target model.Thread
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			ready := false
			f.serv.Do(func() {
				var ok bool
				target, ok = f.C.ThreadByReference(ref)
				ready = ok && !target.Busy && f.C.Collaboration.Reservations[target.ID] == ""
			})
			if ready {
				return target
			}
		}
		t.Fatalf("the conversation %s did not settle: busy %v", ref, target.Busy)
		return target
	}
	storedSession := func() string {
		session := ""
		f.serv.Do(func() {
			for _, b := range f.C.Bots() {
				if b.ID == rin.ID {
					session = b.Session
				}
			}
		})
		return session
	}

	if state := conversation(); state != "new" {
		t.Fatalf("a Bot nobody has written to has the conversation %q, want new", state)
	}

	// A Bot brings its own agent, and only a listed name is a Bot.
	for name, args := range map[string]map[string]any{
		"a Bot with an agent": {"request_id": "with-agent", "bot": "Rin", "agent": appmodel.AgentClaude, "task": "Review the diff", "notify": "none"},
		"an unknown Bot":      {"request_id": "unknown", "bot": "Ren", "task": "Review the diff", "notify": "none"},
	} {
		if _, err := collaborationMCPCall(t, f, caller, "start_task", args); err == nil || !strings.Contains(err.Error(), "INVALID_ARGUMENT") {
			t.Fatalf("%s: %v, want INVALID_ARGUMENT", name, err)
		}
	}

	// The first task starts the conversation. Agents write the name loosely.
	firstArgs := map[string]any{"request_id": "first", "bot": " rin ", "task": "Review the diff", "notify": "none"}
	first, err := collaborationMCPCall(t, f, caller, "start_task", firstArgs)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := first["task_id"].(string)
	if id == "" {
		t.Fatalf("start_task with a Bot returned %v", first)
	}
	// While it runs, a retry is the same task and another task is refused.
	if retry, err := collaborationMCPCall(t, f, caller, "start_task", firstArgs); err != nil || retry["task_id"] != id {
		t.Fatalf("retry while the conversation starts: %v %v, want task %s", retry, err, id)
	}
	if _, err := collaborationMCPCall(t, f, caller, "start_task", map[string]any{"request_id": "eager", "bot": "Rin", "task": "Check the tests", "notify": "none"}); err == nil || !strings.Contains(err.Error(), "THREAD_BUSY") {
		t.Fatalf("a second task while the first runs: %v, want THREAD_BUSY", err)
	}

	done := waitCollaborationTask(t, f, id)
	if done.Bot != rin.ID || !done.NewSession || done.Result.Text != "partial done" {
		t.Fatalf("the first task = %+v, want it to start the conversation of Bot %s", done, rin.ID)
	}
	standing := settled(done.Target)
	if standing.ServerID == "" || storedSession() != standing.ServerID {
		t.Fatalf("the Bot's conversation is %q, the task ran in %q", storedSession(), standing.ServerID)
	}
	var threads int
	f.serv.Do(func() { threads = len(f.C.AllThreads()) })
	if state := conversation(); state != "idle" {
		t.Fatalf("after its first task the conversation is %q, want idle", state)
	}

	// The next task goes to the same conversation and starts no other.
	second, err := collaborationMCPCall(t, f, caller, "start_task", map[string]any{"request_id": "second", "bot": "Rin", "task": "Check the tests", "notify": "none"})
	if err != nil {
		t.Fatal(err)
	}
	nextID, _ := second["task_id"].(string)
	if nextID == "" || nextID == id {
		t.Fatalf("the second task = %v", second)
	}
	again := waitCollaborationTask(t, f, nextID)
	if again.Target != done.Target || again.NewSession || again.Result.Text != "partial done" {
		t.Fatalf("the second task = %+v, want it in the conversation %s", again, done.Target)
	}
	standing = settled(done.Target)
	var after int
	f.serv.Do(func() { after = len(f.C.AllThreads()) })
	if after != threads || storedSession() != standing.ServerID {
		t.Fatalf("%d conversations after the second task, %d before; the Bot's is %q", after, threads, storedSession())
	}

	// The introduction went with the task that started the conversation, once.
	intros, role := 0, false
	for _, m := range standing.Messages {
		intros += strings.Count(m.Content, "[Miya, sent once]")
		role = role || strings.Contains(m.Content, "Reviews pull requests")
	}
	if intros != 1 || !role {
		t.Fatalf("the Bot was introduced %d times, with its role %v; want once with it", intros, role)
	}
}
