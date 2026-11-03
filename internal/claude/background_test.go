package claude

import (
	"encoding/json"
	"testing"
)

func backgroundFrame(t *testing.T, s *backgroundTracker, raw string) Frame {
	t.Helper()
	var f Frame
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		t.Fatal(err)
	}
	s.apply(&f)
	return f
}
func TestBackgroundShellNativeLifecycleAndRunIdentity(t *testing.T) {
	var s backgroundTracker
	backgroundFrame(t, &s, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"bash","name":"Bash","input":{"command":"npm run dev","description":"Run app","run_in_background":true}}]}}`)
	backgroundFrame(t, &s, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"bash","content":"Started"}]},"tool_use_result":{"backgroundTaskId":"1234"}}`)
	if !s.active() || s.known {
		t.Fatal("async acknowledgement was treated as completion or full snapshot")
	}
	backgroundFrame(t, &s, `{"type":"system","subtype":"task_started","task_id":"1234","tool_use_id":"bash","task_type":"local_bash","run_id":"002"}`)
	backgroundFrame(t, &s, `{"type":"system","subtype":"task_notification","task_id":"1234","run_id":"001","status":"completed"}`)
	if !s.active() {
		t.Fatal("late completion ended new run")
	}
	f := backgroundFrame(t, &s, `{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"1234","run_id":"002","task_type":"local_bash","description":"App"},{"task_id":"agent","task_type":"local_agent","description":"Review"}]}`)
	if !s.known || len(s.tasks) != 1 || s.tasks["1234"].Command != "npm run dev" {
		t.Fatal("native set lost tool metadata or mixed agent tasks")
	}
	var agents agentTracker
	agents.apply(&f)
	if len(agents.tasks) != 1 || agents.tasks[0].ID != "agent" {
		t.Fatal("agent tracker mixed shell and agent tasks")
	}
	backgroundFrame(t, &s, `{"type":"system","subtype":"background_tasks_changed","tasks":[]}`)
	if s.active() || !s.known {
		t.Fatal("complete empty snapshot did not reconcile shell tasks")
	}
}
func TestBackgroundShellEventsDoNotBecomeSubagents(t *testing.T) {
	var tasks backgroundTracker
	var agents agentTracker
	f := backgroundFrame(t, &tasks, `{"type":"system","subtype":"task_started","task_id":"shell","task_type":"local_bash","description":"Web server"}`)
	agents.apply(&f)
	if len(agents.tasks) != 0 || !tasks.active() {
		t.Fatal("Bash task was converted into a subagent")
	}
}

func TestLateShellAcknowledgementDoesNotResurrectRemovedTask(t *testing.T) {
	var s backgroundTracker
	backgroundFrame(t, &s, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"bash","name":"Bash","input":{"command":"serve"}}]}}`)
	backgroundFrame(t, &s, `{"type":"system","subtype":"task_started","task_id":"1234","tool_use_id":"bash","task_type":"local_bash","run_id":"001"}`)
	backgroundFrame(t, &s, `{"type":"system","subtype":"background_tasks_changed","tasks":[]}`)
	backgroundFrame(t, &s, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"bash","content":"Started"}]},"tool_use_result":{"backgroundTaskId":"1234"}}`)
	if s.active() {
		t.Fatal("late tool acknowledgement resurrected removed shell")
	}
	backgroundFrame(t, &s, `{"type":"system","subtype":"task_started","task_id":"1234","tool_use_id":"bash","task_type":"local_bash","run_id":"002"}`)
	if !s.active() {
		t.Fatal("new native run was rejected")
	}
}

func TestMalformedSnapshotDoesNotStopNativeWorkAndUnknownKindsRemainVisible(t *testing.T) {
	var s backgroundTracker
	backgroundFrame(t, &s, `{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"future","task_type":"future_task","description":"Future background work"}]}`)
	if !s.known || !s.active() || s.tasks["future"].Kind != "future_task" {
		t.Fatal("unknown native task kind was discarded")
	}
	backgroundFrame(t, &s, `{"type":"system","subtype":"background_tasks_changed"}`)
	if !s.active() {
		t.Fatal("missing tasks field became an empty authoritative snapshot")
	}
}
