package integration

import (
	"encoding/json"
	"path/filepath"
	"testing"

	claude "miya/internal/agent/claude/native"
	"miya/internal/automation"
	"miya/internal/chat/model"
)

// A permission request from an automation run with an access level is
// answered by Miya and never waits for a person; one from an ordinary
// conversation still does.
func TestAutomationRunApprovalsAreAnsweredByMiya(t *testing.T) {
	c := testController()
	dir := t.TempDir()
	rn := &automation.Runner{Store: automation.Load(filepath.Join(t.TempDir(), "automations.json")), Active: map[string]*automation.ActiveRun{}}
	rn.Store.Automations = append(rn.Store.Automations, automation.Automation{ID: "a1", Name: "Deps", Project: dir, Instructions: "x", Access: automation.AccessEdit})
	rn.Store.Runs = map[string][]automation.Run{"a1": {{Result: automation.Running, Thread: 1}}}
	rn.Active["a1"] = &automation.ActiveRun{Thread: 1}
	c.Auto = rn
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "t1", Busy: true, Automation: "a1"}, {ID: 2, ServerID: "t2", Busy: true}})

	ask := func(thread int, id, tool, input string) {
		var f claude.Frame
		raw := `{"type":"control_request","request_id":"` + id + `","request":{"subtype":"can_use_tool","tool_name":"` + tool + `","input":` + input + `}}`
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			t.Fatal(err)
		}
		c.HandleClaude(thread, f)
	}
	ask(1, "r1", "Bash", `{"command":"git push origin main"}`)
	ask(1, "r2", "Edit", `{"file_path":"`+filepath.Join(dir, "go.mod")+`"}`)
	if n := len(c.Approvals()); n != 0 {
		t.Fatalf("an automation run waits for a person: %+v", c.Approvals())
	}
	if refused := rn.Store.Runs["a1"][0].Refused; len(refused) != 1 || refused[0] != "git push origin main" {
		t.Fatalf("refused: %q", refused)
	}
	ask(2, "r3", "Bash", `{"command":"git push origin main"}`)
	if n := len(c.Approvals()); n != 1 {
		t.Fatalf("an ordinary conversation's request: %d cards", n)
	}
}
