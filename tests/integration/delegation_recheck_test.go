//go:build !windows

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	claude "miya/internal/agent/claude/native"
	"miya/internal/appdir"
	"miya/internal/appmodel"
	"miya/internal/async"
	"miya/internal/chat/model"
	"miya/internal/controller"
	dg "miya/internal/delegation"
)

func TestCollaborationClaudeRootResultCanBeRecheckedAfterRestart(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, appmodel.AgentClaude, "source")
	task := collaborationStoredFixtureTask(t, f, caller)
	var targetID int
	target := model.ThreadRef{Source: testReferenceSource, Backend: appmodel.AgentClaude, SessionID: "target"}.URL()
	f.serv.Do(func() {
		c := f.srv.C
		targetID = c.NextID()
		c.AddThread(c.Cwd, model.Thread{ID: targetID, Agent: appmodel.AgentClaude, ServerID: "target", HistoryLoaded: true, Busy: true, ExecutionID: task.ID,
			Messages: []model.Message{{Role: "user", Content: task.Prompt}, {Role: "assistant", Content: "fixed result", ExecutionID: task.ID}}})
		c.Collaboration.Executions = map[string]*controller.ManagedExecution{task.ID: {Thread: targetID, Epoch: "claude:original"}}
		c.Collaboration.Reservations = map[int]string{targetID: task.ID}
	})
	s := f.srv.C.Collaboration
	if _, err := s.Call(func() (any, error) {
		return nil, s.Manager.Observe(dg.Event{TaskID: task.ID, Target: target, SessionEpoch: "claude:original", State: dg.Running})
	}); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(appdir.Dir(), "delegations", "index.json")
	if err := os.Rename(index, index+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(index, 0o700); err != nil {
		t.Fatal(err)
	}
	f.serv.Do(func() {
		c := f.srv.C
		// A child's result must never create parent completion evidence.
		c.HandleClaude(targetID, claude.Frame{Type: "result", UUID: "child-result", ParentToolUseID: "child-call", Result: "wrong result"})
	})
	if _, err := s.Call(func() (any, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Manager.NativeOutcome(task.ID); err == nil {
		t.Fatal("child result created parent terminal evidence")
	}
	f.serv.Do(func() {
		c := f.srv.C
		c.HandleClaude(targetID, claude.Frame{Type: "result", UUID: "root-result", Result: "fixed result"})
		c.Update(targetID, func(t *model.Thread) { t.Busy = false })
	})
	if _, err := s.Call(func() (any, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if s.Manager.StorageError() == "" {
		t.Fatal("result index failure was not reported")
	}
	proof, err := s.Manager.NativeOutcome(task.ID)
	if err != nil || proof.Result.Text != "fixed result" || proof.NativeResultID != "root-result" {
		t.Fatal("adapter did not preserve exact root outcome", proof, err)
	}
	// Repair storage, then restart only this test's collaboration service.
	s.Stop()
	<-s.Done()
	if err := os.Remove(index); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(index+".saved", index); err != nil {
		t.Fatal(err)
	}
	f.serv.Do(func() {
		c := f.srv.C
		c.Collaboration = controller.NewCollaboration(c, filepath.Dir(index))
		c.Collaboration.Executions, c.Collaboration.Reservations = nil, nil
		c.Update(targetID, func(t *model.Thread) {
			t.ExecutionID = ""
			t.Messages = append(t.Messages, model.Message{Role: "assistant", Content: "later unrelated result"})
		})
	})
	current, _ := f.srv.C.Collaboration.Manager.Read(task.ID)
	if current.State != dg.NeedsAttention {
		t.Fatal("restart guessed an uncommitted outcome", current)
	}
	var work any
	f.serv.Do(func() { work, err = f.srv.C.PrepareTaskRecheck(task.From, task.ID) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := work.(async.Reply)(); err != nil {
		t.Fatal(err)
	}
	result, err := collaborationUserCall(t, f, "read_task", task.From, map[string]any{"task_id": task.ID})
	if err != nil || result["state"] != "completed" || result["result"].(map[string]any)["text"] != "fixed result" {
		t.Fatal("recheck mixed in a later conversation", result, err)
	}
}

func TestCollaborationClaudeLiveBindingIsSavedBeforeInput(t *testing.T) {
	f := newServiceFixture(t)
	// Exercise the actual stream adapter with a native root-result UUID.
	var binary string
	f.serv.Do(func() { binary = f.srv.C.ClaudeBinary })
	protocol := strings.Replace(fakeClaude, `"type":"result",`, `"type":"result","uuid":"root-result",`, 1)
	if err := os.WriteFile(binary, []byte(protocol), 0o755); err != nil {
		t.Fatal(err)
	}
	caller := collaborationCaller(t, f, appmodel.AgentClaude, "source")
	result, err := collaborationMCPCall(t, f, caller, "start_task", map[string]any{"request_id": "bound", "agent": appmodel.AgentClaude, "task": "review"})
	if err != nil {
		t.Fatal(err)
	}
	task := waitCollaborationTask(t, f, result["task_id"].(string))
	proof, err := f.srv.C.Collaboration.Manager.NativeOutcome(task.ID)
	if err != nil || task.State != dg.Completed || proof.Target != task.Target || proof.SessionEpoch != task.SessionEpoch || proof.NativeResultID != "root-result" {
		t.Fatal("native result was not bound to its persisted process/session", task, proof, err)
	}
}
