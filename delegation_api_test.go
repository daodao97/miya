//go:build !windows

package main

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

	"miya/internal/appdir"
	"miya/internal/codex"
	dg "miya/internal/delegation"
	"miya/internal/subagent"
)

func collaborationUserCall(t *testing.T, f *serviceFixture, operation, from string, args map[string]any) (map[string]any, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	client, err := dialService(f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var result map[string]any
	err = client.call("collaboration", collaborationRequest{Operation: operation, From: from, Arguments: raw}, &result)
	return result, err
}

func collaborationStoredFixtureTask(t *testing.T, f *serviceFixture, caller int) dg.Task {
	t.Helper()
	var from string
	f.serv.Do(func() { from = f.srv.c.threadRef(f.srv.c.threadWithID(caller)).URL() })
	value, err := f.srv.c.collaboration.call(func() (any, error) {
		return f.srv.c.collaboration.manager.Create(dg.StartInput{From: from, RequestID: "fixture", Agent: agentClaude, Workspace: f.srv.c.cwd, Prompt: "fixture task"})
	})
	if err != nil {
		t.Fatal(err)
	}
	return value.(dg.Task)
}

func TestCollaborationMCPOperationRetriesKeepOriginalReceipt(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, agentClaude, "source")
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
	for _, w := range f.srv.c.collaboration.manager.View().Watches {
		if w.State != "cancelled" {
			t.Fatal("old watch revived observation")
		}
	}
	if task, _ := f.srv.c.collaboration.manager.Read(task.ID); task.CancelRequested {
		t.Fatal("conflicting cancel produced a side effect")
	}
}

func TestCollaborationStopPausesAndExplicitResumeIgnoresOldStopFlag(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, agentClaude, "paused-source")
	var from string
	f.serv.Do(func() {
		c := f.srv.c
		c.update(caller, func(th *Thread) { th.Busy = true; th.LaunchModel = "original"; th.LaunchEffort = "high" })
		from = c.threadRef(c.threadWithID(caller)).URL()
	})
	result, err := collaborationMCPCall(t, f, caller, "start_task", map[string]any{"request_id": "notification", "agent": agentClaude, "task": "Review", "notify": "on_finish"})
	if err != nil {
		t.Fatal(err)
	}
	waitCollaborationTask(t, f, result["task_id"].(string))
	f.serv.Do(func() {
		f.srv.c.stopThread(caller)
		f.srv.c.update(caller, func(th *Thread) { th.Busy = false; th.StopRequested = true })
	})
	if _, err := f.srv.c.collaboration.call(func() (any, error) { f.srv.c.collaboration.tick(); return nil, nil }); err != nil {
		t.Fatal(err)
	}
	list, err := collaborationUserCall(t, f, "list_tasks", from, map[string]any{})
	if err != nil || list["paused"] != true {
		t.Fatalf("Stop pause not visible: %v %v", list, err)
	}
	for _, d := range f.srv.c.collaboration.manager.View().Deliveries {
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
		for _, d := range f.srv.c.collaboration.manager.View().Deliveries {
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
	caller := collaborationCaller(t, f, agentClaude, "storage-source")
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
		state := f.srv.c.snapshotState()
		phone := f.srv.c.phoneSnapshot()
		if state.Collaboration == nil || !state.Collaboration.Stale || phone.Collaboration == nil || !phone.Collaboration.Stale {
			t.Error("storage fault missing from desktop/phone capability state")
		}
		mirror := newMirrorController()
		mirror.applyState(state)
		if !mirror.collaborationCapabilities().Stale {
			t.Error("mirror dropped storage health")
		}
	})
}

func TestCollaborationNativeErrorClassificationDoesNotAssumeNoSideEffect(t *testing.T) {
	for _, tc := range []struct {
		code     int
		rejected bool
	}{{-32600, true}, {-32601, true}, {-32602, true}, {-32603, false}, {-32000, false}} {
		if got := provenCodexRejection(&codex.RPCError{Code: tc.code, Message: "failure"}); got != tc.rejected {
			t.Fatalf("code %d: rejection=%v", tc.code, got)
		}
	}
}

func TestCollaborationCodexResultsUseNativeFinalAnswerPhase(t *testing.T) {
	result := managedResult([]Message{
		{Role: "assistant", Content: "earlier commentary", Item: &codex.Item{Phase: "commentary"}},
		{Role: "assistant", Content: "fixed final", Item: &codex.Item{Phase: "final_answer"}},
		{Role: "assistant", Content: "late commentary", Item: &codex.Item{Phase: "commentary"}},
		{Role: "assistant", Content: "", Item: &codex.Item{Phase: "final_answer"}},
	}, true)
	if result.Text != "fixed final" {
		t.Fatalf("native final phase lost: %+v", result)
	}
	if codexFinalPhase("final") {
		t.Fatal("invented phase admitted as native final")
	}
}

func TestCollaborationSessionSettingsSurviveMetadataCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeThreadListCache(map[string][]Thread{"/project": {{ID: 1, Agent: agentClaude, ServerID: "session", LaunchModel: "fixed", LaunchEffort: "high", PermissionPolicy: "never"}}})
	threads := loadThreadListCache()["/project"]
	if len(threads) != 1 || threads[0].LaunchModel != "fixed" || threads[0].LaunchEffort != "high" || threads[0].PermissionPolicy != "never" {
		t.Fatalf("settings lost: %+v", threads)
	}
}

func TestCollaborationProgressIsBoundedAndStaysInItsExecution(t *testing.T) {
	target := Thread{ExecutionID: "task-current", Messages: []Message{
		{ExecutionID: "task-current", Role: "assistant", Content: strings.Repeat("中", 4000) + " latest"},
		{ExecutionID: "task-current", Role: "reasoning", Content: "private reasoning"},
		{ExecutionID: "task-previous", Role: "assistant", Content: "another execution"},
		{ExecutionID: "task-current", Role: "tool", Content: "tool output", Agents: []SubAgent{{Status: subagent.Running}}},
	}}
	progress, wait := managedProgress(target)
	if !utf8.ValidString(progress) || len([]rune(progress)) > 1024 || !strings.HasSuffix(progress, " latest") || strings.Contains(progress, "private") || strings.Contains(progress, "another") || wait != "sub_agent" {
		t.Fatalf("unsafe progress: %q %s", progress, wait)
	}
}

func TestCollaborationCorruptStoreDoesNotPanicStopOrLegacyTools(t *testing.T) {
	f := newServiceFixture(t)
	callerID := collaborationCaller(t, f, agentClaude, "source")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	var readErr, createErr error
	var capability *collaborationStatus
	f.serv.Do(func() {
		c := f.srv.c
		c.collaboration.cancel()
		c.collaboration = newCollaboration(c, dir)
		caller := c.threadWithID(callerID)
		c.stopThread(callerID)
		_, readErr = c.legacyTaskRead(mcpRequest{TaskID: "task-unavailable"}, caller)
		_, createErr = c.legacyDelegate(mcpRequest{Arguments: json.RawMessage(`{"task":"test"}`)}, caller)
		capability = c.collaborationCapabilities()
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
	callerID := collaborationCaller(t, f, agentCodex, "source")
	var from string
	f.serv.Do(func() { from = f.srv.c.threadRef(f.srv.c.threadWithID(callerID)).URL() })
	s := f.srv.c.collaboration
	value, err := s.call(func() (any, error) {
		return s.manager.Create(dg.StartInput{From: from, RequestID: "early", Agent: agentCodex, Workspace: f.srv.c.cwd, Prompt: "test"})
	})
	if err != nil {
		t.Fatal(err)
	}
	task := value.(dg.Task)
	var targetID int
	f.serv.Do(func() {
		c := f.srv.c
		targetID = c.nextID()
		worker := Thread{ID: targetID, Agent: agentCodex, ServerID: "worker", ExecutionID: task.ID, Busy: true, HistoryLoaded: true}
		c.addThread(c.cwd, worker)
		c.managedExecutions = map[string]*managedExecution{task.ID: {thread: targetID}}
		c.managedReservations = map[int]string{targetID: task.ID}
		for i := 0; i < 9; i++ {
			c.managedCodexOutput(worker, "turn/completed", codex.Event{Turn: codex.Turn{ID: fmt.Sprint(i), Status: "completed"}})
		}
		c.managedCodexAccepted(task.ID, "intended-turn", "live-epoch", nil, true)
		if run := c.managedExecutions[task.ID]; !run.pendingOverflow || len(run.pending) != 0 {
			t.Error("unbound event buffer grew without limit")
		}
	})
	t.Cleanup(func() { f.serv.Do(func() { f.srv.c.update(targetID, func(th *Thread) { th.Busy = false }) }) })
	value, err = s.call(func() (any, error) { return s.manager.Read(task.ID) })
	if err != nil {
		t.Fatal(err)
	}
	got := value.(dg.Task)
	if got.State != dg.NeedsAttention || got.NativeTurnID != "intended-turn" || got.Result.Text != "" {
		t.Fatalf("overflow invented a result: %+v", got)
	}
}
