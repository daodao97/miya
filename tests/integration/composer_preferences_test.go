package integration

import (
	"encoding/json"
	"testing"

	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/controller"
	"miya/internal/prefs"
	"miya/internal/uiruntime"
)

func TestProjectThreadsSortBeforeCollapse(t *testing.T) {
	all := []model.Thread{{ID: 1, LastActivityAt: 1}, {ID: 2, LastActivityAt: 8}, {ID: 3, LastActivityAt: 3}, {ID: 4, LastActivityAt: 8}, {ID: 5, LastActivityAt: 2}, {ID: 6, LastActivityAt: 9}}
	got := msgfmt.DisplayedThreads(all, false)
	want := []int{6, 2, 4, 3, 5}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("order: %+v", got)
		}
	}
	if all[0].ID != 1 {
		t.Fatal("sort mutated controller state")
	}
	if len(msgfmt.DisplayedThreads(all, true)) != 6 {
		t.Fatal("expanded list lost a thread")
	}
}

func TestCodexEffortSurvivesModelSelection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var m codex.Model
	if e := json.Unmarshal([]byte(`{"model":"model-a","defaultReasoningEffort":"medium","supportedReasoningEfforts":[{"reasoningEffort":"medium"},{"reasoningEffort":"high"}]}`), &m); e != nil {
		t.Fatal(e)
	}
	c := &controller.Controller{}
	c.Model, c.SetModel = uiruntime.CreateSignal("model-a")
	c.Effort, c.SetEffort = uiruntime.CreateSignal("medium")
	c.Models, _ = uiruntime.CreateSignal([]codex.Model{m})
	c.BackendFor(model.AgentCodex).SetEffort("high")
	c.SetEffort("medium")
	c.SelectModel("model-a")
	if c.Effort() != "high" {
		t.Fatal("saved effort was replaced by model default")
	}
	m.Model = "model-b"
	if controller.SavedCodexEffort(m) != "medium" {
		t.Fatal("effort leaked to another model")
	}
	m.Model = "model-a"
	m.SupportedReasoningEfforts = m.SupportedReasoningEfforts[:1]
	if controller.SavedCodexEffort(m) != "medium" {
		t.Fatal("unsupported saved effort was reused")
	}
}

func TestCodexFastCommandDoesNotSendPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := testController()
	for _, tc := range []struct{ command, want string }{{"fast", "fast"}, {"fast", "default"}, {"fast on", "fast"}, {"fast off", "default"}} {
		_, message := c.ThreadCommand(0, appmodel.AgentCodex, tc.command, "")
		if prefs.Load("codexServiceTier") != tc.want || message != "" {
			t.Fatalf("%s: %q", tc.command, message)
		}
	}
	if len(c.Threads()) != 2 || len(c.Threads()[0].Messages) != 0 {
		t.Fatal("fast command sent a message")
	}
	c.ThreadCommand(0, appmodel.AgentClaude, "fast on", "")
	if prefs.Load("codexServiceTier") != "default" {
		t.Fatal("Claude command changed Codex preference")
	}
}
