package integration

import (
	"encoding/json"
	"miya/internal/agent"
	"miya/internal/chat/model"
	"miya/internal/controller"
	"miya/internal/interaction"
	phoneproto "miya/internal/phone/protocol"
	phoneserver "miya/internal/phone/server"
	"miya/internal/remote"
	"testing"
)

type externalControls struct {
	agent.Controls
	catalog agent.CommandCatalog
	loads   []bool
}

func (a *externalControls) Model() string { return "external-model" }

func (a *externalControls) Effort() string { return "external-effort" }

func (a *externalControls) Commands(_ string, load bool) agent.CommandCatalog {
	a.loads = append(a.loads, load)
	return a.catalog
}

func TestAdaptersKeepWorkspaceStateSeparate(t *testing.T) {
	first := &controller.Controller{ClaudeModel: "first", ClaudeCommand: "first-cli"}
	second := &controller.Controller{ClaudeModel: "second", ClaudeCommand: "second-cli"}
	a, b := first.BackendFor(model.AgentClaude), second.BackendFor(model.AgentClaude)
	if a.Model() != "first" || b.Model() != "second" {
		t.Fatal("adapter model crossed workspaces")
	}
	if a.Settings()[0].Value() != "first-cli" || b.Settings()[0].Value() != "second-cli" {
		t.Fatal("settings captured another workspace")
	}
	first.ClaudeModel = "changed"
	if a.Model() != "changed" || b.Model() != "second" {
		t.Fatal("adapter froze state or shared it globally")
	}
}

func TestTypedPhoneReplyUsesSharedValidationAndAdapterEncoding(t *testing.T) {
	c := newViewerController()
	defer c.Cancel()
	request := interaction.Request{ID: json.RawMessage(`"claude:1:q"`), Method: "item/tool/requestUserInput", Questions: []interaction.Question{{ID: "q", Question: "Continue?"}}}
	c.SetApprovals([]interaction.Request{request})
	invalid := interaction.Response{Action: interaction.Submit, Answers: map[string]string{"wrong": "yes"}}
	err := c.PhoneApprovalResponse(remote.Request{Approval: request.ID, Response: &invalid})
	if err == nil || len(c.Approvals()) != 1 {
		t.Fatal("invalid typed answers bypassed validation")
	}
	// No Claude run is required for stale request cleanup; the adapter owns native dispatch.
	valid := interaction.Response{Action: interaction.Submit, Answers: map[string]string{"q": "yes"}}
	if err := c.PhoneApprovalResponse(remote.Request{Approval: request.ID, Response: &valid}); err != nil || len(c.Approvals()) != 0 {
		t.Fatalf("typed reply failed: %v approvals=%v", err, c.Approvals())
	}
}

func TestApprovalWireCompatibilityAndFingerprint(t *testing.T) {
	request := interaction.Request{ID: json.RawMessage(`7`), Method: "item/commandExecution/requestApproval", Command: "pwd"}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["agent"]; ok {
		t.Fatal("legacy request gained an agent wire field")
	}
	if _, ok := fields["kind"]; ok {
		t.Fatal("legacy request gained a kind wire field")
	}
	original := phoneproto.ApprovalFingerprint(request)
	request.Category = interaction.Questions
	if original == phoneproto.ApprovalFingerprint(request) {
		t.Fatal("changed interaction semantics retained a stale fingerprint")
	}
}

func TestPresentationOnlyChangesReachPhone(t *testing.T) {
	sync := phoneserver.NewThreadSync(nil)
	thread := model.Thread{ID: 1, Messages: []model.Message{{ID: 1, Role: "tool", Content: "native state", Presentation: &model.MessagePresentation{Content: "first", Title: "External tool"}}}}
	if delta, changed := sync.Update(thread); !changed || len(delta.Changed) != 1 {
		t.Fatal("initial projection not sent")
	}
	thread.Messages[0].Presentation = &model.MessagePresentation{Content: "second", Title: "External tool"}
	if delta, changed := sync.Update(thread); !changed || len(delta.Changed) != 1 || delta.Changed[0].Presentation.Content != "second" {
		t.Fatal("presentation-only update was lost")
	}
	if _, changed := sync.Update(thread); changed {
		t.Fatal("unchanged projection was sent again")
	}
}
