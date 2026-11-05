//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"miya/internal/appdir"
	"miya/internal/claude"
	dg "miya/internal/delegation"
)

func TestCollaborationClaudeRootResultCanBeRecheckedAfterRestart(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, agentClaude, "source")
	task := collaborationStoredFixtureTask(t, f, caller)
	var targetID int
	target := ThreadRef{Source: testReferenceSource, Backend: agentClaude, SessionID: "target"}.URL()
	f.serv.Do(func() {
		c := f.srv.c
		targetID = c.nextID()
		c.addThread(c.cwd, Thread{ID: targetID, Agent: agentClaude, ServerID: "target", HistoryLoaded: true, Busy: true, ExecutionID: task.ID,
			Messages: []Message{{Role: "user", Content: task.Prompt}, {Role: "assistant", Content: "fixed result", ExecutionID: task.ID}}})
		c.managedExecutions = map[string]*managedExecution{task.ID: {thread: targetID, epoch: "claude:original"}}
		c.managedReservations = map[int]string{targetID: task.ID}
	})
	s := f.srv.c.collaboration
	if _, err := s.call(func() (any, error) {
		return nil, s.manager.Observe(dg.Event{TaskID: task.ID, Target: target, SessionEpoch: "claude:original", State: dg.Running})
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
		c := f.srv.c
		// A child's result must never create parent completion evidence.
		c.handleClaude(targetID, claude.Frame{Type: "result", UUID: "child-result", ParentToolUseID: "child-call", Result: "wrong result"})
	})
	if _, err := s.call(func() (any, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.manager.NativeOutcome(task.ID); err == nil {
		t.Fatal("child result created parent terminal evidence")
	}
	f.serv.Do(func() {
		c := f.srv.c
		c.handleClaude(targetID, claude.Frame{Type: "result", UUID: "root-result", Result: "fixed result"})
		c.update(targetID, func(t *Thread) { t.Busy = false })
	})
	if _, err := s.call(func() (any, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if s.manager.StorageError() == "" {
		t.Fatal("result index failure was not reported")
	}
	proof, err := s.manager.NativeOutcome(task.ID)
	if err != nil || proof.Result.Text != "fixed result" || proof.NativeResultID != "root-result" {
		t.Fatal("adapter did not preserve exact root outcome", proof, err)
	}
	// Repair storage, then restart only this test's collaboration service.
	s.cancel()
	<-s.done
	if err := os.Remove(index); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(index+".saved", index); err != nil {
		t.Fatal(err)
	}
	f.serv.Do(func() {
		c := f.srv.c
		c.collaboration = newCollaboration(c, filepath.Dir(index))
		c.managedExecutions, c.managedReservations = nil, nil
		c.update(targetID, func(t *Thread) {
			t.ExecutionID = ""
			t.Messages = append(t.Messages, Message{Role: "assistant", Content: "later unrelated result"})
		})
	})
	current, _ := f.srv.c.collaboration.manager.Read(task.ID)
	if current.State != dg.NeedsAttention {
		t.Fatal("restart guessed an uncommitted outcome", current)
	}
	var work any
	f.serv.Do(func() { work, err = f.srv.c.prepareTaskRecheck(task.From, task.ID) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := work.(deferred)(); err != nil {
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
	f.serv.Do(func() { binary = f.srv.c.claudeBinary })
	protocol := strings.Replace(fakeClaude, `"type":"result",`, `"type":"result","uuid":"root-result",`, 1)
	if err := os.WriteFile(binary, []byte(protocol), 0o755); err != nil {
		t.Fatal(err)
	}
	caller := collaborationCaller(t, f, agentClaude, "source")
	result, err := collaborationMCPCall(t, f, caller, "start_task", map[string]any{"request_id": "bound", "agent": agentClaude, "task": "review"})
	if err != nil {
		t.Fatal(err)
	}
	task := waitCollaborationTask(t, f, result["task_id"].(string))
	proof, err := f.srv.c.collaboration.manager.NativeOutcome(task.ID)
	if err != nil || task.State != dg.Completed || proof.Target != task.Target || proof.SessionEpoch != task.SessionEpoch || proof.NativeResultID != "root-result" {
		t.Fatal("native result was not bound to its persisted process/session", task, proof, err)
	}
}
