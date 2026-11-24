//go:build !windows

package integration

import (
	"slices"
	"strings"
	"testing"
	"time"

	"miya/internal/appmodel"
	"miya/internal/bot"
	"miya/internal/chat/model"
)

// An agent hands tasks to a Bot by the name list_agents gave. Each task starts
// a new conversation of the Bot, so the Bot takes a task while it works on
// another, and each conversation is recorded as the Bot's. The charter goes
// to the agent with the message, not into the visible conversation.
func TestCollaborationTasksStartConversationsOfABot(t *testing.T) {
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

	// A Bot brings its own agent, and only a listed name is a Bot.
	for name, args := range map[string]map[string]any{
		"a Bot with an agent": {"request_id": "with-agent", "bot": "Rin", "agent": appmodel.AgentClaude, "task": "Review the diff", "notify": "none"},
		"an unknown Bot":      {"request_id": "unknown", "bot": "Ren", "task": "Review the diff", "notify": "none"},
	} {
		if _, err := collaborationMCPCall(t, f, caller, "start_task", args); err == nil || !strings.Contains(err.Error(), "INVALID_ARGUMENT") {
			t.Fatalf("%s: %v, want INVALID_ARGUMENT", name, err)
		}
	}

	// Agents write the name loosely; a retry is the same task.
	firstArgs := map[string]any{"request_id": "first", "bot": " rin ", "task": "Review the diff", "notify": "none"}
	first, err := collaborationMCPCall(t, f, caller, "start_task", firstArgs)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := first["task_id"].(string)
	if id == "" {
		t.Fatalf("start_task with a Bot returned %v", first)
	}
	if retry, err := collaborationMCPCall(t, f, caller, "start_task", firstArgs); err != nil || retry["task_id"] != id {
		t.Fatalf("retry: %v %v, want task %s", retry, err, id)
	}
	// The Bot takes another task while it works on the first.
	second, err := collaborationMCPCall(t, f, caller, "start_task", map[string]any{"request_id": "second", "bot": "Rin", "task": "Check the tests", "notify": "none"})
	if err != nil {
		t.Fatalf("a second task while the first runs: %v", err)
	}
	nextID, _ := second["task_id"].(string)
	if nextID == "" || nextID == id {
		t.Fatalf("the second task = %v", second)
	}

	done := waitCollaborationTask(t, f, id)
	again := waitCollaborationTask(t, f, nextID)
	for _, task := range []struct {
		name        string
		bot, target string
		new         bool
		result      string
	}{{"first", done.Bot, done.Target, done.NewSession, done.Result.Text}, {"second", again.Bot, again.Target, again.NewSession, again.Result.Text}} {
		if task.bot != rin.ID || !task.new || task.target == "" || task.result != "partial done" {
			t.Fatalf("the %s task: Bot %q, new %v, target %q, result %q; want a new conversation of %s", task.name, task.bot, task.new, task.target, task.result, rin.ID)
		}
	}
	if done.Target == again.Target {
		t.Fatalf("both tasks ran in %s", done.Target)
	}

	// Both conversations are the Bot's; the person's own is still to start.
	var sessions []string
	for _, ref := range []string{done.Target, again.Target} {
		var target model.Thread
		f.serv.Do(func() { target, _ = f.C.ThreadByReference(ref) })
		if target.ServerID == "" {
			t.Fatalf("the conversation %s has no session", ref)
		}
		for _, m := range target.Messages {
			if strings.Contains(m.Content, "[Miya] In this conversation") || strings.Contains(m.Content, "Reviews pull requests") {
				t.Fatalf("the charter shows in the conversation: %q", m.Content)
			}
		}
		sessions = append(sessions, target.ServerID)
	}
	var stored bot.Bot
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		f.serv.Do(func() {
			for _, b := range f.C.Bots() {
				if b.ID == rin.ID {
					stored = b
				}
			}
		})
		if slices.Contains(stored.Others, sessions[0]) && slices.Contains(stored.Others, sessions[1]) {
			break
		}
	}
	if stored.Session != "" || !slices.Contains(stored.Others, sessions[0]) || !slices.Contains(stored.Others, sessions[1]) {
		t.Fatalf("the Bot records %q and %v, want %v among its other conversations", stored.Session, stored.Others, sessions)
	}
}
