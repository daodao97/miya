//go:build !mygo_ui

package integration

import (
	"encoding/json"
	codex "miya/internal/agent/codex/native"
	mcpaccess "miya/internal/mcp/access"
	"testing"
)

func appAccessRequest() codex.Approval {
	return codex.Approval{ID: json.RawMessage(`1`), Method: "mcpServer/elicitation/request", ThreadID: "thread-a", TurnID: "turn-1", ServerName: "cua_repl", Mode: "form", Message: `Allow Computer Use to use "Xcode"?`, RequestedSchema: json.RawMessage(`{"type":"object","properties":{}}`)}
}

func TestMCPSessionGrantRequiresExplicitLiveResponse(t *testing.T) {
	c := testController()
	a := appAccessRequest()
	if c.RememberedMCPAccess(a) {
		t.Fatal("permission granted before user response")
	}
	if c.RespondMCPAlways(a) == nil {
		t.Fatal("stale request created grant")
	}
	c.SetApprovals([]codex.Approval{a})
	if err := c.RespondMCPAlways(a); err != nil {
		t.Fatal(err)
	}
	if len(c.Approvals()) != 0 {
		t.Fatal("accepted request remained pending")
	}
	a.ID = json.RawMessage(`2`)
	a.TurnID = "turn-2"
	if !c.RememberedMCPAccess(a) {
		t.Fatal("grant did not survive the next turn")
	}
	if testController().RememberedMCPAccess(a) {
		t.Fatal("grant persisted beyond backend lifetime")
	}
	for _, change := range []func(*codex.Approval){
		func(a *codex.Approval) { a.ThreadID = "thread-b" },
		func(a *codex.Approval) { a.ServerName = "other-server" },
		func(a *codex.Approval) { a.Message = `Allow Computer Use to use "Terminal"?` },
		func(a *codex.Approval) { a.Mode = "url"; a.URL = "https://example.com/login" },
		func(a *codex.Approval) { a.Message = "Confirm this transaction" },
		func(a *codex.Approval) {
			a.RequestedSchema = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`)
		},
		func(a *codex.Approval) {
			a.RequestedSchema = json.RawMessage(`{"type":"object","properties":{},"unknown":true}`)
		},
	} {
		different := a
		change(&different)
		if c.RememberedMCPAccess(different) {
			t.Fatalf("grant exceeded scope: %+v", different)
		}
	}
}

func TestMCPSessionGrantUsesTrustedQueuedRequest(t *testing.T) {
	c := testController()
	a := appAccessRequest()
	c.SetApprovals([]codex.Approval{a})
	submitted := a
	submitted.ThreadID = "forged-thread"
	submitted.Message = `Allow Computer Use to use "Terminal"?`
	if err := c.RespondMCPAlways(submitted); err != nil {
		t.Fatal(err)
	}
	if c.RememberedMCPAccess(submitted) || !c.RememberedMCPAccess(a) {
		t.Fatal("grant used UI-supplied scope instead of live request")
	}
}

func TestMCPSessionPermissionSchemaCanonicalization(t *testing.T) {
	a := appAccessRequest()
	b := a
	b.RequestedSchema = json.RawMessage(`{ "properties": {}, "type": "object" }`)
	if mcpaccess.Key(a) != mcpaccess.Key(b) {
		t.Fatal("JSON formatting changed grant identity")
	}
}
