package integration

import (
	"encoding/json"
	codex "miya/internal/agent/codex/native"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	"testing"
)

func TestMobileFormBuildsTypedValues(t *testing.T) {
	a := codex.Approval{RequestedSchema: json.RawMessage(`{"type":"object","required":["name","count","enabled","mode"],"properties":{"name":{"type":"string"},"count":{"type":"integer"},"enabled":{"type":"boolean"},"mode":{"type":"string","enum":["a","b"]}}}`)}
	values, err := phoneproto.ValidateMobileForm(a, map[string]string{"name": "hello", "count": "2", "enabled": "false", "mode": `"b"`})
	if err != nil || values["count"] != float64(2) || values["enabled"] != false || values["mode"] != "b" {
		t.Fatalf("wrong form types: %+v %v", values, err)
	}
	for _, fields := range []map[string]string{{"name": "hello"}, {"name": "hello", "count": "2.5", "enabled": "false", "mode": `"b"`}} {
		if _, err := phoneproto.ValidateMobileForm(a, fields); err == nil {
			t.Fatal("incomplete or invalid form was accepted")
		}
	}
}

func TestPhoneApprovalValidatesBeforeRemovingCard(t *testing.T) {
	c := testController()
	a := codex.Approval{ID: json.RawMessage(`1`), Method: "item/tool/requestUserInput", Questions: []codex.Question{{ID: "q", Question: "名字？"}}}
	c.SetApprovals([]codex.Approval{a})
	if err := c.PhoneApprovalResponse(remote.Request{Approval: a.ID, Result: json.RawMessage(`{"answers":{"wrong":{"answers":["x"]}}}`)}); err == nil || len(c.Approvals()) != 1 {
		t.Fatal("invalid answer consumed the request")
	}
	if err := c.PhoneApprovalResponse(remote.Request{Approval: a.ID, Result: json.RawMessage(`{"answers":{"q":{"answers":["hello"]}}}`)}); err != nil || len(c.Approvals()) != 0 {
		t.Fatal("valid answer was refused", err)
	}
	if err := c.PhoneApprovalResponse(remote.Request{Approval: a.ID, Allow: true}); err == nil {
		t.Fatal("stale request was accepted")
	}
	a = codex.Approval{ID: json.RawMessage(`2`), Method: "mcpServer/elicitation/request", RequestedSchema: json.RawMessage(`{"type":"object","properties":{"enabled":{"type":"boolean"}},"required":["enabled"]}`)}
	c.SetApprovals([]codex.Approval{a})
	if err := c.PhoneApprovalResponse(remote.Request{Approval: a.ID, Result: json.RawMessage(`{"action":"accept","content":{"enabled":"true"}}`)}); err == nil || len(c.Approvals()) != 1 {
		t.Fatal("invalid MCP form was accepted")
	}
	if err := c.PhoneApprovalResponse(remote.Request{Approval: a.ID, Result: json.RawMessage(`{"action":"accept","content":{"enabled":true}}`)}); err != nil {
		t.Fatal(err)
	}
}
