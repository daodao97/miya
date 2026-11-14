package client

import (
	"encoding/json"
	"testing"
	"time"

	"miya/internal/automation"
	"miya/internal/chat/model"
	"miya/internal/delegation"
	"miya/internal/interaction"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
)

func featureJSON(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }
func TestApprovalReplyDoesNotResolveSupersedingVersion(t *testing.T) {
	m := New()
	m.Connected = true
	sent := make(chan remote.Request, 3)
	m.SendRequest = func(r remote.Request) error { sent <- r; return nil }
	a := interaction.Request{ID: json.RawMessage(`"1"`), Command: "go test", Method: "item/commandExecution/requestApproval"}
	m.State.Approvals = []interaction.Request{a}
	m.SubmitApproval(a, interaction.Response{Action: interaction.Allow}, false)
	<-sent
	next := a
	next.Command = "rm -rf build"
	m.ApplyPhoneState(phoneproto.State{Approvals: []interaction.Request{next}})
	m.ApprovalAnswered(a.ID, "")
	if len(m.State.Approvals) != 1 || m.State.Approvals[0].Command != next.Command {
		t.Fatal("old approval acknowledged a changed command")
	}
	m.SubmitApproval(a, interaction.Response{Action: interaction.Allow}, false)
	select {
	case r := <-sent:
		t.Fatalf("stale card sent %+v", r)
	default:
	}
	m.Close()
}
func TestFeatureDisconnectAndDesktopIsolation(t *testing.T) {
	m := New()
	m.Connected = true
	m.State.Collaboration = &model.CollaborationStatus{Version: 1}
	sent := make(chan remote.Request, 2)
	m.SendRequest = func(r remote.Request) error { sent <- r; return nil }
	done := false
	m.CallCollaboration(delegation.UserRequest{Operation: "list_tasks"}, func(_ json.RawMessage, err error) { done = err != nil })
	<-sent
	m.Autos.Form = AutomationForm{Pending: "create-id", Op: "automation-create", CreateKey: "create-id", CreatePayload: json.RawMessage(`{"name":"original"}`)}
	m.Autos.Loading = true
	m.Memories.Loading = true
	m.Disconnect()
	if !done || len(m.CollaborationCalls) != 0 || m.Autos.Loading || m.Memories.Loading || m.Autos.Form.Pending != "" {
		t.Fatal("pending operations survived disconnect")
	}
	if m.Autos.Form.CreateKey != "create-id" || len(m.Autos.Form.CreatePayload) == 0 {
		t.Fatal("uncertain create identity discarded")
	}
	m.Forget()
	if m.Autos.Form.CreateKey != "" || len(m.Memories.List.Entries) > 0 {
		t.Fatal("another desktop could inherit data")
	}
}
func TestFeatureTimeoutsRetainCreateIdentity(t *testing.T) {
	m := New()
	old := time.Now().Add(-5 * time.Minute)
	m.Autos.Form = AutomationForm{Pending: "key", Op: "automation-create", RequestedAt: old, CreateKey: "key", CreatePayload: json.RawMessage(`{}`)}
	m.Autos.Loading = true
	m.Autos.RequestedAt = old
	m.Autos.RunRequest = "run"
	m.Autos.RunRequestedAt = old
	m.Memories.Loading = true
	m.Memories.RequestedAt = old
	m.TickFeatures()
	if m.Autos.Form.Pending != "" || m.Autos.Form.CreateKey != "key" || m.Autos.Form.Error == "" || m.Autos.RunRequest != "" || m.Autos.Loading || m.Memories.Loading {
		t.Fatal("timeout did not make safe retry available")
	}
}
func TestAutomationRunLookupKeepsStableBackendIdentity(t *testing.T) {
	m := New()
	m.State.Projects = []model.ProjectSnapshot{{Path: "/work", Threads: []model.Thread{{ID: 1, Agent: "claude", ServerID: "session"}, {ID: 99, Agent: "codex", ServerID: "session"}, {ID: 7, Agent: "codex", ServerID: "other"}}}}
	got, ok := m.AutomationRunThread(automation.View{Automation: automation.Automation{Project: "/work", Agent: "codex"}}, automation.Run{Thread: 7, ServerID: "session"})
	if !ok || got.ID != 99 {
		t.Fatalf("lookup = %+v %v", got, ok)
	}
}
