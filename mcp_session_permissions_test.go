package main

import (
	json "encoding/json"
	appmodel "miya/internal/appmodel"
	codex "miya/internal/codex"
	controller "miya/internal/controller"
	testing "testing"
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
	if appmodel.McpSessionPermissionKey(a) != appmodel.McpSessionPermissionKey(b) {
		t.Fatal("JSON formatting changed grant identity")
	}
}

func TestMCPApplicationAccessCardAllowsForSession(t *testing.T) {
	app, tt := testApp(t)
	a := appAccessRequest()
	app.C.(*controller.Controller).SetApprovals([]codex.Approval{a})
	tt.Frame()
	if !tt.HasText("Always allow in this conversation") || !tt.HasText("Allow once") {
		t.Fatalf("missing choices: %q", tt.Texts())
	}
	if tt.HasText("MCP form JSON") || tt.HasText("{}") {
		t.Fatal("application access showed an empty JSON form")
	}
	if err := tt.Click("Always allow in this conversation"); err != nil {
		t.Fatal(err)
	}
	if !app.C.(*controller.Controller).RememberedMCPAccess(a) || len(app.C.(*controller.Controller).Approvals()) != 0 {
		t.Fatal("explicit click did not grant session access")
	}
}

func TestMCPAllowOnceDoesNotCreateSessionGrant(t *testing.T) {
	app, tt := testApp(t)
	a := appAccessRequest()
	app.C.(*controller.Controller).SetApprovals([]codex.Approval{a})
	tt.Frame()
	if err := tt.Click("Allow once"); err != nil {
		t.Fatal(err)
	}
	if app.C.(*controller.Controller).RememberedMCPAccess(a) || len(app.C.(*controller.Controller).Approvals()) != 0 {
		t.Fatal("allow once did not remain a one-time response")
	}
}
