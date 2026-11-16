package controller

import (
	"encoding/json"
	"path/filepath"
	"testing"

	codex "miya/internal/agent/codex/native"
	"miya/internal/automation"
	"miya/internal/chat/model"
	"miya/internal/uiruntime"
)

// codexRunController has an external automation's run going in a Codex
// conversation of a background project.
func codexRunController(t *testing.T, access automation.Access) *Controller {
	t.Helper()
	dir := t.TempDir()
	c := &Controller{Cwd: t.TempDir(), ProjectCache: map[string][]model.Thread{}}
	c.Approvals, c.SetApprovals = uiruntime.CreateSignal([]codex.Approval{})
	c.Threads, c.SetThreads = uiruntime.CreateSignal([]model.Thread{})
	c.Status, c.SetStatus = uiruntime.CreateSignal("")
	c.ProjectCache[dir] = []model.Thread{{ID: 7, ServerID: "srv7", Agent: model.AgentCodex, Automation: "a1"}}
	c.Auto = automation.NewRunner(automationHost{c}, filepath.Join(t.TempDir(), "automations.json"))
	c.Auto.Store.Automations = []automation.Automation{{ID: "a1", Name: "Balance", Project: dir, Access: access, Rounds: 1}}
	c.Auto.Active["a1"] = &automation.ActiveRun{Thread: 7}
	return c
}

// Codex 0.169 sends its own "kind" with a command approval. The request is
// still an approval, so an external automation's run sends it for review
// instead of leaving Codex's card waiting for the person.
func TestCodexCommandApprovalWithKindIsAnsweredForAutomation(t *testing.T) {
	c := codexRunController(t, automation.AccessExternal)
	id, _ := json.Marshal(42)
	c.ServerRequest(codex.Frame{ID: id, Method: "item/commandExecution/requestApproval",
		Params: json.RawMessage(`{"threadId":"srv7","turnId":"u1","itemId":"i1","kind":"command","command":"/bin/zsh -lc 'python monitor.py'","cwd":"/tmp","reason":"needs the MySQL tunnel"}`)})
	if len(c.Auto.Store.Reviews) != 1 || c.Auto.Store.Reviews[0].Action != "/bin/zsh -lc 'python monitor.py'" {
		t.Fatalf("reviews: %+v", c.Auto.Store.Reviews)
	}
	cards := c.Approvals()
	if len(cards) != 1 || cards[0].Method != "miya/review" {
		t.Fatalf("cards: %+v", cards)
	}
}

// Codex asking before one of Miya's tools runs, in an automation's run, is
// answered by Miya: no card waits for the person.
func TestCodexMiyaToolFormIsAnsweredForAutomation(t *testing.T) {
	c := codexRunController(t, automation.AccessRead)
	id, _ := json.Marshal(43)
	c.ServerRequest(codex.Frame{ID: id, Method: "mcpServer/elicitation/request",
		Params: json.RawMessage(`{"threadId":"srv7","turnId":"u1","serverName":"miya","mode":"form","message":"Allow the miya MCP server to run tool \"report_result\"?","requestedSchema":{"type":"object","properties":{}}}`)})
	if cards := c.Approvals(); len(cards) != 0 {
		t.Fatalf("cards: %+v", cards)
	}
	if len(c.Auto.Store.Reviews) != 0 {
		t.Fatalf("reviews: %+v", c.Auto.Store.Reviews)
	}
	// Another server's form still waits for the person.
	id, _ = json.Marshal(44)
	c.ServerRequest(codex.Frame{ID: id, Method: "mcpServer/elicitation/request",
		Params: json.RawMessage(`{"threadId":"srv7","turnId":"u1","serverName":"other","mode":"form","message":"Allow the other MCP server to run tool \"x\"?","requestedSchema":{"type":"object","properties":{}}}`)})
	if cards := c.Approvals(); len(cards) != 1 {
		t.Fatalf("another server's form: %+v", cards)
	}
}
