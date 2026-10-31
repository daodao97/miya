package main

import (
	"encoding/json"
	"miya/internal/codex"
	"testing"
)

func TestProjectThreadsSortBeforeCollapse(t *testing.T) {
	all := []Thread{{ID: 1, LastActivityAt: 1}, {ID: 2, LastActivityAt: 8}, {ID: 3, LastActivityAt: 3}, {ID: 4, LastActivityAt: 8}, {ID: 5, LastActivityAt: 2}, {ID: 6, LastActivityAt: 9}}
	got := displayedThreads(all, false)
	want := []int{6, 2, 4, 3, 5}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("order: %+v", got)
		}
	}
	if all[0].ID != 1 {
		t.Fatal("sort mutated controller state")
	}
	if len(displayedThreads(all, true)) != 6 {
		t.Fatal("expanded list lost a thread")
	}
}
func TestCodexEffortSurvivesModelSelection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var m codex.Model
	if e := json.Unmarshal([]byte(`{"model":"model-a","defaultReasoningEffort":"medium","supportedReasoningEfforts":[{"reasoningEffort":"medium"},{"reasoningEffort":"high"}]}`), &m); e != nil {
		t.Fatal(e)
	}
	c := &chatController{}
	c.model, c.setModel = createSignal("model-a")
	c.effort, c.setEffort = createSignal("medium")
	c.models, _ = createSignal([]codex.Model{m})
	codexBackend{}.SetEffort(c, "high")
	c.setEffort("medium")
	c.selectModel("model-a")
	if c.effort() != "high" {
		t.Fatal("saved effort was replaced by model default")
	}
	m.Model = "model-b"
	if savedCodexEffort(m) != "medium" {
		t.Fatal("effort leaked to another model")
	}
	m.Model = "model-a"
	m.SupportedReasoningEfforts = m.SupportedReasoningEfforts[:1]
	if savedCodexEffort(m) != "medium" {
		t.Fatal("unsupported saved effort was reused")
	}
}
func TestCodexFastCommandDoesNotSendPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := testController()
	for _, tc := range []struct{ command, want string }{{"fast", "fast"}, {"fast", "default"}, {"fast on", "fast"}, {"fast off", "default"}} {
		_, message := c.threadCommand(0, agentCodex, tc.command, "")
		if loadSetting("codexServiceTier") != tc.want || message != "" {
			t.Fatalf("%s: %q", tc.command, message)
		}
	}
	if len(c.threads()) != 2 || len(c.threads()[0].Messages) != 0 {
		t.Fatal("fast command sent a message")
	}
	c.threadCommand(0, agentClaude, "fast on", "")
	if loadSetting("codexServiceTier") != "default" {
		t.Fatal("Claude command changed Codex preference")
	}
}
