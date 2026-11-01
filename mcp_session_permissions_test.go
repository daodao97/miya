package main

import (
	"encoding/json"
	"miya/internal/codex"
	"testing"
)

func appAccessRequest() codex.Approval {
	return codex.Approval{ID: json.RawMessage(`1`), Method: "mcpServer/elicitation/request", ThreadID: "thread-a", TurnID: "turn-1", ServerName: "cua_repl", Mode: "form", Message: `Allow Computer Use to use "Xcode"?`, RequestedSchema: json.RawMessage(`{"type":"object","properties":{}}`)}
}
func TestMCPSessionGrantRequiresExplicitLiveResponse(t *testing.T) {
	c := testController()
	a := appAccessRequest()
	if c.rememberedMCPAccess(a) {
		t.Fatal("permission granted before user response")
	}
	if c.respondMCPAlways(a) == nil {
		t.Fatal("stale request created grant")
	}
	c.setApprovals([]codex.Approval{a})
	if err := c.respondMCPAlways(a); err != nil {
		t.Fatal(err)
	}
	if len(c.approvals()) != 0 {
		t.Fatal("accepted request remained pending")
	}
	a.ID = json.RawMessage(`2`)
	a.TurnID = "turn-2"
	if !c.rememberedMCPAccess(a) {
		t.Fatal("grant did not survive the next turn")
	}
	if testController().rememberedMCPAccess(a) {
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
		if c.rememberedMCPAccess(different) {
			t.Fatalf("grant exceeded scope: %+v", different)
		}
	}
}
func TestMCPSessionGrantUsesTrustedQueuedRequest(t *testing.T) {
	c := testController()
	a := appAccessRequest()
	c.setApprovals([]codex.Approval{a})
	submitted := a
	submitted.ThreadID = "forged-thread"
	submitted.Message = `Allow Computer Use to use "Terminal"?`
	if err := c.respondMCPAlways(submitted); err != nil {
		t.Fatal(err)
	}
	if c.rememberedMCPAccess(submitted) || !c.rememberedMCPAccess(a) {
		t.Fatal("grant used UI-supplied scope instead of live request")
	}
}
func TestMCPSessionPermissionSchemaCanonicalization(t *testing.T) {
	a := appAccessRequest()
	b := a
	b.RequestedSchema = json.RawMessage(`{ "properties": {}, "type": "object" }`)
	if mcpSessionPermissionKey(a) != mcpSessionPermissionKey(b) {
		t.Fatal("JSON formatting changed grant identity")
	}
}

func TestMCPApplicationAccessCardAllowsForSession(t *testing.T) {
	app, tt := testApp(t)
	a := appAccessRequest()
	app.c.setApprovals([]codex.Approval{a})
	tt.Frame()
	if !tt.HasText("此对话始终允许") || !tt.HasText("允许一次") {
		t.Fatalf("missing choices: %q", tt.Texts())
	}
	if tt.HasText("MCP form JSON") || tt.HasText("{}") {
		t.Fatal("application access showed an empty JSON form")
	}
	if err := tt.Click("此对话始终允许"); err != nil {
		t.Fatal(err)
	}
	if !app.c.rememberedMCPAccess(a) || len(app.c.approvals()) != 0 {
		t.Fatal("explicit click did not grant session access")
	}
}
func TestMCPAllowOnceDoesNotCreateSessionGrant(t *testing.T) {
	app, tt := testApp(t)
	a := appAccessRequest()
	app.c.setApprovals([]codex.Approval{a})
	tt.Frame()
	if err := tt.Click("允许一次"); err != nil {
		t.Fatal(err)
	}
	if app.c.rememberedMCPAccess(a) || len(app.c.approvals()) != 0 {
		t.Fatal("allow once did not remain a one-time response")
	}
}
