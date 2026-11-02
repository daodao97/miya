package claude

import (
	"encoding/json"
	"testing"
)

func agentFrame(t *testing.T, s *agentTracker, raw string) Frame {
	t.Helper()
	var f Frame
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		t.Fatal(err)
	}
	s.apply(&f)
	return f
}

func TestAutomaticBackgroundAgentWaitsForNotification(t *testing.T) {
	var s agentTracker
	agentFrame(t, &s, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call1","name":"Agent","input":{"description":"Review"}}]}}`)
	f := agentFrame(t, &s, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call1","content":[{"type":"text","text":"Started"}]}]},"tool_use_result":{"isAsync":true,"status":"async_launched","agentId":"a1"}}`)
	if !f.WaitingForAgents || len(f.AgentTasks) != 1 || f.AgentTasks[0].ID != "a1" || f.AgentTasks[0].Status != "running" {
		t.Fatalf("async ack completed agent: %+v", f)
	}
	agentFrame(t, &s, `{"type":"result","parent_tool_use_id":"call1"}`)
	if !s.active() {
		t.Fatal("child result completed parent task")
	}
	f = agentFrame(t, &s, `{"type":"system","subtype":"task_progress","task_id":"a1","description":"Reading","usage":{"tool_uses":3}}`)
	if f.AgentTasks[0].Steps != 3 || f.AgentTasks[0].Detail != "Reading" {
		t.Fatalf("lost progress: %+v", f.AgentTasks)
	}
	f = agentFrame(t, &s, `{"type":"system","subtype":"task_notification","task_id":"a1","status":"completed","summary":"No issues"}`)
	if f.WaitingForAgents || s.active() || f.AgentTasks[0].Status != "completed" {
		t.Fatalf("lost completion: %+v", f.AgentTasks)
	}
	// Some transcript streams repeat acknowledgements after notifications.
	f = agentFrame(t, &s, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call1","content":"Async agent launched successfully.\nagentId: a1"}]}}`)
	if s.active() || f.AgentTasks[0].Status != "completed" {
		t.Fatal("repeated ack reopened completed task")
	}
}

func TestAgentTrackerSyncFailedAndNativeNotification(t *testing.T) {
	for _, tc := range []struct{ result, status string }{
		{`{"type":"tool_result","tool_use_id":"a","content":"Done"}`, "completed"},
		{`{"type":"tool_result","tool_use_id":"a","is_error":true,"content":"Failed"}`, "failed"},
		{`{"type":"tool_result","tool_use_id":"a","content":"Async agent launched successfully.\nagentId: native1"}`, "running"},
	} {
		var s agentTracker
		agentFrame(t, &s, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"Task","input":{}}]}}`)
		f := agentFrame(t, &s, `{"type":"user","message":{"content":[`+tc.result+`]}}`)
		if f.AgentTasks[0].Status != tc.status {
			t.Fatalf("%s: %+v", tc.result, f.AgentTasks)
		}
		if tc.status == "running" {
			f = agentFrame(t, &s, `{"type":"user","message":{"content":"<task-notification><task-id>native1</task-id><status>failed</status><summary>Failure</summary></task-notification>"}}`)
			if s.active() || f.AgentTasks[0].Status != "failed" {
				t.Fatalf("native notification not applied: %+v", f.AgentTasks)
			}
		}
	}
	for _, text := range []string{"Example <task-notification><task-id>x</task-id><status>completed</status></task-notification>", "<task-notification><status>completed</status></task-notification>", "<task-notification>malformed"} {
		if _, ok := TaskNotification(text); ok {
			t.Fatalf("parsed literal/invalid notification: %q", text)
		}
	}
}
