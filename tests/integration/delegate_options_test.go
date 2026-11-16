//go:build !windows

package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	mcpproto "miya/internal/mcp/protocol"
	svcclient "miya/internal/service/client"
)

func delegateMCPCall(t *testing.T, f *serviceFixture, caller int, args map[string]any) (mcpproto.DelegateResult, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	var messageID int
	f.serv.Do(func() {
		messageID = f.C.NextID()
		f.C.Update(caller, func(t *model.Thread) {
			t.Messages = append(t.Messages, model.Message{ID: messageID, Role: "tool", Kind: "claudeTool", Content: "mcp__miya__delegate\n" + string(raw), Status: "inProgress"})
		})
	})
	client, err := svcclient.Dial(f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var result mcpproto.DelegateResult
	err = client.Call("mcpDelegate", mcpproto.Request{Tool: "delegate", Arguments: raw}, &result)
	f.serv.Do(func() {
		f.C.Update(caller, func(t *model.Thread) {
			for i := range t.Messages {
				if t.Messages[i].ID == messageID {
					t.Messages[i].Status = "completed"
				}
			}
		})
	})
	return result, err
}

// delegate takes a model and thinking choice from list_agents, and the new
// conversation it opens runs with full permission, never waiting on an
// approval, whatever the window's permission choice is.
func TestDelegateChoosesModelAndRunsWithFullPermission(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, appmodel.AgentClaude, "initiator")
	listed, err := collaborationMCPCall(t, f, caller, "list_agents", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var chosenModel, chosenEffort string
	agents, _ := listed["agents"].([]any)
	for _, a := range agents {
		agent, _ := a.(map[string]any)
		if agent["id"] != appmodel.AgentClaude {
			continue
		}
		for _, m := range agent["models"].([]any) {
			if s, _ := m.(string); s != "" {
				chosenModel = s
			}
		}
		for _, e := range agent["efforts"].([]any) {
			if s, _ := e.(string); s != "" {
				chosenEffort = s
			}
		}
	}

	if _, err := delegateMCPCall(t, f, caller, map[string]any{"task": "x", "agent": appmodel.AgentClaude, "model": "no-such-model", "include_context": false}); err == nil || !strings.Contains(err.Error(), "Model is unavailable") {
		t.Fatalf("unknown model accepted: %v", err)
	}

	args := map[string]any{"task": "Review compatibility", "agent": appmodel.AgentClaude, "include_context": false}
	if chosenModel != "" {
		args["model"] = chosenModel
	}
	if chosenEffort != "" {
		args["effort"] = chosenEffort
	}
	res, err := delegateMCPCall(t, f, caller, args)
	if err != nil {
		t.Fatal(err)
	}
	if res.TaskID == "" {
		t.Fatalf("delegate did not create a task: %+v", res)
	}
	task, err := f.C.Collaboration.Manager.Read(res.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Config.Policy != "never" {
		t.Fatalf("new conversation policy = %q, want never", task.Config.Policy)
	}
	if chosenModel != "" && (task.Config.Model != chosenModel || task.Requested.Model != chosenModel) {
		t.Fatalf("model not applied: %+v %+v", task.Config, task.Requested)
	}
	if chosenEffort != "" && task.Config.Effort != chosenEffort {
		t.Fatalf("effort not applied: %+v", task.Config)
	}
	done := waitCollaborationTask(t, f, res.TaskID)
	var policy string
	var found bool
	f.serv.Do(func() {
		var th model.Thread
		if th, found = f.C.ThreadByReference(done.Target); found {
			policy = th.PermissionPolicy
		}
	})
	if !found || policy != "never" {
		t.Fatalf("delegated conversation %q permission = %q (found %v), want never", done.Target, policy, found)
	}
}
