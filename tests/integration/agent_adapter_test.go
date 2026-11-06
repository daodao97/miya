package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"miya/internal/agent"
	"miya/internal/agentmeta"
	"miya/internal/controller"
	"miya/internal/conversation"
	"miya/internal/interaction"
	"miya/internal/model"
	"miya/internal/msgfmt"
	"miya/internal/phone"
	"miya/internal/phoneapi"
	"miya/internal/remote"
	desktopui "miya/internal/ui"
)

type externalControls struct {
	agent.Controls
	catalog agent.CommandCatalog
	loads   []bool
}

func (a *externalControls) Model() string  { return "external-model" }
func (a *externalControls) Effort() string { return "external-effort" }

func (a *externalControls) Commands(_ string, load bool) agent.CommandCatalog {
	a.loads = append(a.loads, load)
	return a.catalog
}

type externalHost struct {
	desktopui.Host
	controls agent.Controls
	sent     bool
}

func (h *externalHost) CurrentAgentControls() agent.Controls { return h.controls }
func (h *externalHost) CurrentAgent() string                 { return "third-party" }
func (h *externalHost) SendDraft(bool) int                   { h.sent = true; return 0 }

func TestDesktopUsesThirdAgentCommandContract(t *testing.T) {
	c := newViewerController()
	defer c.Cancel()
	controls := &externalControls{catalog: agent.CommandCatalog{Native: true, Known: true, Choices: []model.ComposerChoice{{Name: "custom-build", Detail: "Third agent native command"}}}}
	host := &externalHost{Host: c, controls: controls}
	app := &desktopui.App{C: host}
	choices := app.CommandChoices("BUILD")
	if len(choices) != 1 || choices[0].Name != "custom-build" || len(controls.loads) != 1 || !controls.loads[0] {
		t.Fatalf("third agent catalog not used: %+v / %+v", choices, controls.loads)
	}
	c.SetDraft("/custom-build target")
	app.SendDraft(false)
	if !host.sent || app.Notice != "" {
		t.Fatalf("native command did not reach third agent: sent=%v notice=%q", host.sent, app.Notice)
	}
	c.SetModel("another-agent-model")
	c.SetDraft("/status")
	app.SendDraft(false)
	if !strings.Contains(app.Notice, "external-model") || !strings.Contains(app.Notice, "external-effort") || strings.Contains(app.Notice, "another-agent-model") {
		t.Fatalf("status read another agent's selection: %q", app.Notice)
	}

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

func TestMobileUsesThirdAgentDescriptorAndCommands(t *testing.T) {
	m := testMobileApp()
	m.State.Agents = []phoneapi.AgentOptions{{Agent: "third-party", Descriptor: agentmeta.Descriptor{ID: "third-party", Name: "External Agent"}, Commands: []model.ComposerChoice{{Name: "custom-build", Detail: "Build with external agent"}}}}
	m.NewAgent = "third-party"
	m.Draft = "/custom"
	tt := ui.NewTester(m.ViewRoot, 390, 750)
	tt.Frame()
	if _, ok := tt.Find("/custom-build"); !ok {
		t.Fatalf("third agent command absent: %v", tt.Texts())
	}
}

func TestNeutralMessagePresentationOverridesNativePayload(t *testing.T) {
	message := model.Message{ID: 1, Role: "tool", Kind: "external", Content: "opaque provider envelope", Presentation: &model.MessagePresentation{Title: "External tool", Icon: "terminal-2", Content: "normalized result", Format: "text"}}
	if got := msgfmt.MessageGroupText(msgfmt.MessageGroup{Messages: []model.Message{message}}); got != "normalized result" {
		t.Fatalf("copy exposed native data: %q", got)
	}
	view := conversation.NewConversationView(newViewerController())
	view.Open[1] = true
	tt := ui.NewTester(func(c *ui.Context) { view.MessageView(c, message, false) }, 600, 300)
	if !tt.HasText("External tool") || !tt.HasText("normalized result") || tt.HasText("opaque provider envelope") {
		t.Fatalf("view decoded native data: %v", tt.Texts())
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
	original := phoneapi.ApprovalFingerprint(request)
	request.Category = interaction.Questions
	if original == phoneapi.ApprovalFingerprint(request) {
		t.Fatal("changed interaction semantics retained a stale fingerprint")
	}
}

func TestPresentationOnlyChangesReachPhone(t *testing.T) {
	sync := phone.NewThreadSync(nil)
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
