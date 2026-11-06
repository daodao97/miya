//go:build !windows

package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	codex "miya/internal/agent/codex/native"
	"miya/internal/appdir"
	"miya/internal/appmodel"
	threadlist "miya/internal/chat/history"
	"miya/internal/chat/model"
	"miya/internal/chat/subagent"
	"miya/internal/controller"
	dg "miya/internal/delegation"
	"miya/internal/delegation/coordinator"
	mcpproto "miya/internal/mcp/protocol"
	svcclient "miya/internal/service/client"
)

func collaborationUserCall(t *testing.T, f *serviceFixture, operation, from string, args map[string]any) (map[string]any, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	client, err := svcclient.Dial(f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var result map[string]any
	err = client.Call("collaboration", appmodel.CollaborationRequest{Operation: operation, From: from, Arguments: raw}, &result)
	return result, err
}

func collaborationStoredFixtureTask(t *testing.T, f *serviceFixture, caller int) dg.Task {
	t.Helper()
	var from string
	f.serv.Do(func() {
		from = f.C.ThreadRef(f.C.ThreadWithID(caller)).URL()
	})
	value, err := f.C.Collaboration.Call(func() (any, error) {
		return f.C.Collaboration.Manager.Create(dg.StartInput{From: from, RequestID: "fixture", Agent: appmodel.AgentClaude, Workspace: f.C.Cwd, Prompt: "fixture task"})
	})
	if err != nil {
		t.Fatal(err)
	}
	return value.(dg.Task)
}

func TestCollaborationMCPOperationRetriesKeepOriginalReceipt(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, appmodel.AgentClaude, "source")
	task := collaborationStoredFixtureTask(t, f, caller)
	watch := map[string]any{"request_id": "watch", "task_id": task.ID}
	first, err := collaborationMCPCall(t, f, caller, "watch_task", watch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collaborationMCPCall(t, f, caller, "unwatch_task", map[string]any{"request_id": "unwatch", "task_id": task.ID}); err != nil {
		t.Fatal(err)
	}
	retry, err := collaborationMCPCall(t, f, caller, "watch_task", watch)
	if err != nil || !reflect.DeepEqual(first, retry) {
		t.Fatalf("retry changed receipt: %v %v", retry, err)
	}
	if _, err := collaborationMCPCall(t, f, caller, "cancel_task", watch); err == nil || !strings.Contains(err.Error(), "REQUEST_CONFLICT") {
		t.Fatal(err)
	}
	for _, w := range f.C.Collaboration.Manager.View().Watches {
		if w.State != "cancelled" {
			t.Fatal("old watch revived observation")
		}
	}
	if task, _ := f.C.Collaboration.Manager.Read(task.ID); task.CancelRequested {
		t.Fatal("conflicting cancel produced a side effect")
	}
}

func TestCollaborationStopPausesAndExplicitResumeIgnoresOldStopFlag(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, appmodel.AgentClaude, "paused-source")
	var from string
	f.serv.Do(func() {
		c := f.C
		c.Update(caller, func(th *model.Thread) { th.Busy = true; th.LaunchModel = "original"; th.LaunchEffort = "high" })
		from = c.ThreadRef(c.ThreadWithID(caller)).URL()
	})
	result, err := collaborationMCPCall(t, f, caller, "start_task", map[string]any{"request_id": "notification", "agent": appmodel.AgentClaude, "task": "Review", "notify": "on_finish"})
	if err != nil {
		t.Fatal(err)
	}
	waitCollaborationTask(t, f, result["task_id"].(string))
	f.serv.Do(func() {
		f.C.StopThread(caller)
		f.C.Update(caller, func(th *model.Thread) { th.Busy = false; th.StopRequested = true })
	})
	if _, err := f.C.Collaboration.Call(func() (any, error) { f.C.Collaboration.Tick(); return nil, nil }); err != nil {
		t.Fatal(err)
	}
	list, err := collaborationUserCall(t, f, "list_tasks", from, map[string]any{})
	if err != nil || list["paused"] != true {
		t.Fatalf("Stop pause not visible: %v %v", list, err)
	}
	for _, d := range f.C.Collaboration.Manager.View().Deliveries {
		if d.State != "pending" {
			t.Fatal("Stop allowed a new native turn")
		}
	}
	resumed, err := collaborationUserCall(t, f, "resume", from, nil)
	if err != nil || resumed["paused"] != false {
		t.Fatalf("resume: %v %v", resumed, err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		for _, d := range f.C.Collaboration.Manager.View().Deliveries {
			if d.State == "delivered" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("explicit collaboration resume remained blocked by the old Stop flag")
}

func TestCollaborationWriteFailureIsVisibleInQueriesAndState(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, appmodel.AgentClaude, "storage-source")
	task := collaborationStoredFixtureTask(t, f, caller)
	index := filepath.Join(appdir.Dir(), "delegations", "index.json")
	if err := os.Rename(index, index+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(index, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := collaborationUserCall(t, f, "cancel_task", task.From, map[string]any{"request_id": "cancel", "task_id": task.ID})
	if err == nil || !strings.Contains(err.Error(), "STORAGE_UNAVAILABLE") {
		t.Fatal(err)
	}
	result, err := collaborationUserCall(t, f, "read_task", task.From, map[string]any{"task_id": task.ID})
	message, hasMessage := result["storage_error"].(string)
	if err != nil || result["stale"] != true || !hasMessage || message == "" || result["cancel_requested"] != false {
		t.Fatalf("stale query: %v %v", result, err)
	}
	f.serv.Do(func() {
		state := f.C.SnapshotState()
		phone := f.C.PhoneSnapshot()
		if state.Collaboration == nil || !state.Collaboration.Stale || phone.Collaboration == nil || !phone.Collaboration.Stale {
			t.Error("storage fault missing from desktop/phone capability state")
		}
		mirror := controller.NewMirrorController()
		mirror.ApplyState(state)
		if !mirror.CollaborationCapabilities().Stale {
			t.Error("mirror dropped storage health")
		}
	})
}

func TestCollaborationNativeErrorClassificationDoesNotAssumeNoSideEffect(t *testing.T) {
	for _, tc := range []struct {
		code     int
		rejected bool
	}{{-32600, true}, {-32601, true}, {-32602, true}, {-32603, false}, {-32000, false}} {
		if got := coordinator.ProvenCodexRejection(&codex.RPCError{Code: tc.code, Message: "failure"}); got != tc.rejected {
			t.Fatalf("code %d: rejection=%v", tc.code, got)
		}
	}
}

func TestCollaborationCodexResultsUseNativeFinalAnswerPhase(t *testing.T) {
	result := coordinator.NativeResult([]model.Message{
		{Role: "assistant", Content: "earlier commentary", Item: &model.Activity{Phase: "commentary"}},
		{Role: "assistant", Content: "fixed final", Item: &model.Activity{Phase: "final_answer"}},
		{Role: "assistant", Content: "late commentary", Item: &model.Activity{Phase: "commentary"}},
		{Role: "assistant", Content: "", Item: &model.Activity{Phase: "final_answer"}},
	}, true)
	if result.Text != "fixed final" {
		t.Fatalf("native final phase lost: %+v", result)
	}
	if coordinator.CodexFinalPhase("final") {
		t.Fatal("invented phase admitted as native final")
	}
}

func TestCollaborationSessionSettingsSurviveMetadataCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	threadlist.Write(map[string][]model.Thread{"/project": {{ID: 1, Agent: appmodel.AgentClaude, ServerID: "session", LaunchModel: "fixed", LaunchEffort: "high", PermissionPolicy: "never"}}})
	threads := threadlist.Load()["/project"]
	if len(threads) != 1 || threads[0].LaunchModel != "fixed" || threads[0].LaunchEffort != "high" || threads[0].PermissionPolicy != "never" {
		t.Fatalf("settings lost: %+v", threads)
	}
}

func TestCollaborationProgressIsBoundedAndStaysInItsExecution(t *testing.T) {
	target := model.Thread{ExecutionID: "task-current", Messages: []model.Message{
		{ExecutionID: "task-current", Role: "assistant", Content: strings.Repeat("中", 4000) + " latest"},
		{ExecutionID: "task-current", Role: "reasoning", Content: "private reasoning"},
		{ExecutionID: "task-previous", Role: "assistant", Content: "another execution"},
		{ExecutionID: "task-current", Role: "tool", Content: "tool output", Agents: []model.SubAgent{{Status: subagent.Running}}},
	}}
	progress, wait := coordinator.ThreadProgress(target)
	if !utf8.ValidString(progress) || len([]rune(progress)) > 1024 || !strings.HasSuffix(progress, " latest") || strings.Contains(progress, "private") || strings.Contains(progress, "another") || wait != "sub_agent" {
		t.Fatalf("unsafe progress: %q %s", progress, wait)
	}
}

func TestCollaborationCorruptStoreDoesNotPanicStopOrLegacyTools(t *testing.T) {
	f := newServiceFixture(t)
	callerID := collaborationCaller(t, f, appmodel.AgentClaude, "source")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	var readErr, createErr error
	var capability *model.CollaborationStatus
	f.serv.Do(func() {
		c := f.C
		c.Collaboration.Stop()
		c.Collaboration = controller.NewCollaboration(c, dir)
		caller := c.ThreadWithID(callerID)
		c.StopThread(callerID)
		_, readErr = c.MCP().LegacyTaskRead(mcpproto.Request{TaskID: "task-unavailable"}, caller)
		_, createErr = c.MCP().LegacyDelegate(mcpproto.Request{Arguments: json.RawMessage(`{"task":"test"}`)}, caller)
		capability = c.CollaborationCapabilities()
	})
	if readErr == nil || createErr == nil || !strings.Contains(readErr.Error(), "STORAGE_UNAVAILABLE") || !strings.Contains(createErr.Error(), "STORAGE_UNAVAILABLE") || capability == nil || !capability.Stale {
		t.Fatalf("corrupt-store fallback: %v %v %+v", readErr, createErr, capability)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "index.json")); err != nil || string(data) != "broken" {
		t.Fatal("corrupt store replaced")
	}
}

func TestCollaborationUnboundTurnBufferIsBoundedAndRequiresRecheck(t *testing.T) {
	f := newServiceFixture(t)
	callerID := collaborationCaller(t, f, appmodel.AgentCodex, "source")
	var from string
	f.serv.Do(func() {
		from = f.C.ThreadRef(f.C.ThreadWithID(callerID)).URL()
	})
	s := f.C.Collaboration
	value, err := s.Call(func() (any, error) {
		return s.Manager.Create(dg.StartInput{From: from, RequestID: "early", Agent: appmodel.AgentCodex, Workspace: f.C.Cwd, Prompt: "test"})
	})
	if err != nil {
		t.Fatal(err)
	}
	task := value.(dg.Task)
	var targetID int
	f.serv.Do(func() {
		c := f.C
		targetID = c.NextID()
		worker := model.Thread{ID: targetID, Agent: appmodel.AgentCodex, ServerID: "worker", ExecutionID: task.ID, Busy: true, HistoryLoaded: true}
		c.AddThread(c.Cwd, worker)
		c.Collaboration.Executions = map[string]*controller.ManagedExecution{task.ID: {Thread: targetID}}
		c.Collaboration.Reservations = map[int]string{targetID: task.ID}
		for i := 0; i < 9; i++ {
			c.ManagedCodexOutput(worker, "turn/completed", codex.Event{Turn: codex.Turn{ID: fmt.Sprint(i), Status: "completed"}})
		}
		c.ManagedCodexAccepted(task.ID, "intended-turn", "live-epoch", nil, true)
		if run := c.Collaboration.Executions[task.ID]; !run.PendingOverflow || len(run.Pending) != 0 {
			t.Error("unbound event buffer grew without limit")
		}
	})
	t.Cleanup(func() {
		f.serv.Do(func() {
			f.C.Update(targetID, func(th *model.Thread) { th.Busy = false })
		})
	})
	value, err = s.Call(func() (any, error) { return s.Manager.Read(task.ID) })
	if err != nil {
		t.Fatal(err)
	}
	got := value.(dg.Task)
	if got.State != dg.NeedsAttention || got.NativeTurnID != "intended-turn" || got.Result.Text != "" {
		t.Fatalf("overflow invented a result: %+v", got)
	}
}
