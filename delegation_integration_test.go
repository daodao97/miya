//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"miya/internal/appdir"
	"miya/internal/codex"
	dg "miya/internal/delegation"
	"miya/internal/delegation/coordinator"
)

func collaborationCaller(t *testing.T, f *serviceFixture, agent, serverID string) int {
	t.Helper()
	var id int
	f.serv.Do(func() {
		c := f.srv.c
		id = c.nextID()
		c.addThread(c.cwd, Thread{ID: id, Agent: agent, ServerID: serverID, Title: "Initiator", HistoryLoaded: true, Loaded: true, Resumed: true})
	})
	return id
}

func collaborationMCPCall(t *testing.T, f *serviceFixture, caller int, name string, args map[string]any) (map[string]any, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	var messageID int
	f.serv.Do(func() {
		c := f.srv.c
		messageID = c.nextID()
		c.update(caller, func(t *Thread) {
			t.Messages = append(t.Messages, Message{ID: messageID, Role: "tool", Kind: "claudeTool", Content: "mcp__miya__" + name + "\n" + string(raw), Status: "inProgress"})
		})
	})
	client, err := dialService(f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var result map[string]any
	err = client.call("mcpCollaboration", mcpRequest{Tool: name, Arguments: raw}, &result)
	f.serv.Do(func() {
		f.srv.c.update(caller, func(t *Thread) {
			for i := range t.Messages {
				if t.Messages[i].ID == messageID {
					t.Messages[i].Status = "completed"
				}
			}
		})
	})
	return result, err
}

func waitCollaborationTask(t *testing.T, f *serviceFixture, id string) dg.Task {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		task, err := f.srv.c.collaboration.Manager.Read(id)
		if err == nil && dg.Terminal(task.State) {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, err := f.srv.c.collaboration.Manager.Read(id)
	t.Fatalf("task did not terminate: %+v, %v", task, err)
	return dg.Task{}
}

func TestCollaborationClaudeTasksRunIndependentlyAndFreezeResults(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, agentClaude, "initiator")
	first, err := collaborationMCPCall(t, f, caller, "start_task", map[string]any{"request_id": "one", "agent": agentClaude, "task": "Review compatibility"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := collaborationMCPCall(t, f, caller, "start_task", map[string]any{"request_id": "two", "agent": agentClaude, "task": "Check tests"})
	if err != nil {
		t.Fatal(err)
	}
	aID, bID := first["task_id"].(string), second["task_id"].(string)
	if aID == bID {
		t.Fatal("two tasks share identity")
	}
	var busy int
	f.serv.Do(func() {
		for _, th := range f.srv.c.allThreads() {
			if th.ExecutionID == aID || th.ExecutionID == bID {
				if th.Busy {
					busy++
				}
			}
		}
	})
	if busy != 2 {
		t.Fatalf("independent native sessions not running together: %d", busy)
	}
	a, b := waitCollaborationTask(t, f, aID), waitCollaborationTask(t, f, bID)
	if a.Target == b.Target || a.Result.Text != "partial done" || b.Result.Text != "partial done" {
		t.Fatalf("bad native results: %+v %+v", a, b)
	}
	// Retrying the request must not create another native session.
	retry, err := collaborationMCPCall(t, f, caller, "start_task", map[string]any{"request_id": "one", "agent": agentClaude, "task": "Review compatibility"})
	if err != nil || retry["task_id"] != aID {
		t.Fatalf("retry: %v %v", retry, err)
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		ready := false
		f.serv.Do(func() {
			target, ok := f.srv.c.threadByReference(a.Target)
			ready = ok && !target.Busy && f.srv.c.collaboration.Reservations[target.ID] == ""
		})
		if ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var sent int
	f.serv.Do(func() {
		target, _ := f.srv.c.threadByReference(a.Target)
		sent = f.srv.c.submit(SendRequest{ThreadID: target.ID, Text: "A later unrelated message"})
	})
	if sent == 0 {
		t.Fatal("ordinary follow-up rejected")
	}
	got, err := collaborationMCPCall(t, f, caller, "read_task", map[string]any{"task_id": aID})
	if err != nil {
		t.Fatal(err)
	}
	if got["state"] != "completed" || got["result"].(map[string]any)["text"] != "partial done" {
		t.Fatalf("follow-up changed frozen result: %+v", got)
	}
}

func TestCollaborationCodexBindsNativeTurnBeforeReturningFixedResult(t *testing.T) {
	f := newServiceFixture(t)
	var dir string
	f.serv.Do(func() { dir = f.srv.c.cwd })
	bin, err := filepath.Abs("testdata/codex-server.py")
	if err != nil {
		t.Fatal(err)
	}
	client, err := codex.Start(context.Background(), bin, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var source codex.ThreadResponse
	if err := client.Call(context.Background(), "thread/start", map[string]any{"cwd": dir}, &source); err != nil {
		t.Fatal(err)
	}
	f.serv.Do(func() { f.srv.c.client = client; f.srv.c.setReady(true) })
	go func() {
		for event := range client.Events() {
			f.srv.c.dispatch(func() { f.srv.c.handle(event) })
		}
	}()
	caller := collaborationCaller(t, f, agentCodex, source.Thread.ID)
	result, err := collaborationMCPCall(t, f, caller, "start_task", map[string]any{"request_id": "native", "agent": agentCodex, "task": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	task := waitCollaborationTask(t, f, result["task_id"].(string))
	if task.NativeTurnID == "" || task.Target == "" || task.State != dg.Completed || task.Result.Text == "" {
		t.Fatalf("native execution not bound: %+v", task)
	}
	loaded, err := dg.NewStore(filepath.Join(appdir.Dir(), "delegations")).Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Tasks[task.ID].NativeTurnID != task.NativeTurnID {
		t.Fatal("result identity not published")
	}
}

func TestCollaborationMCPRejectsAmbiguousCallerBeforeCreatingTasks(t *testing.T) {
	f := newServiceFixture(t)
	a := collaborationCaller(t, f, agentClaude, "a")
	b := collaborationCaller(t, f, agentClaude, "b")
	args := map[string]any{"request_id": "same", "agent": agentClaude, "task": "same request"}
	raw, _ := json.Marshal(args)
	f.serv.Do(func() {
		c := f.srv.c
		for _, id := range []int{a, b} {
			c.update(id, func(th *Thread) {
				th.Messages = append(th.Messages, Message{Role: "tool", Kind: "claudeTool", Status: "inProgress", Content: "mcp__miya__start_task\n" + string(raw)})
			})
		}
	})
	client, err := dialService(f.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var out any
	if err := client.call("mcpCollaboration", mcpRequest{Tool: "start_task", Arguments: raw}, &out); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous call accepted: %v", err)
	}
	if len(f.srv.c.collaboration.Manager.View().Tasks) != 0 {
		t.Fatal("ambiguous caller produced a side effect")
	}
}

func TestCollaborationNewToolsValidateArgumentsAndBoundWait(t *testing.T) {
	for _, raw := range []string{`{"request_id":"x","task":"hi","surprise":true}`, `{"request_id":"x"}`, `[]`, `{"request_id":null,"task":"hi"}`} {
		if _, err := coordinator.Decode("start_task", json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid arguments accepted: %s", raw)
		}
	}
	if _, err := coordinator.Decode("wait_task", json.RawMessage(`{"task_id":"x","wait_seconds":121}`)); err == nil {
		t.Fatal("unbounded wait accepted")
	}
}

func TestCollaborationCompletionNotificationWaitsForIdleAndStartsOneTurn(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, agentClaude, "notify-source")
	f.serv.Do(func() { f.srv.c.update(caller, func(th *Thread) { th.Busy = true }) })
	result, err := collaborationMCPCall(t, f, caller, "start_task", map[string]any{"request_id": "notify", "agent": agentClaude, "task": "Review compatibility", "notify": "on_finish"})
	if err != nil {
		t.Fatal(err)
	}
	waitCollaborationTask(t, f, result["task_id"].(string))
	if len(f.srv.c.collaboration.Manager.View().Deliveries) != 1 {
		t.Fatal("no durable completion notification")
	}
	var notices int
	f.serv.Do(func() {
		for _, m := range f.srv.c.threadWithID(caller).Messages {
			if m.Role == "user" && strings.HasPrefix(m.Content, "[Miya collaboration]") {
				notices++
			}
		}
	})
	if notices != 0 {
		t.Fatal("busy parent received another turn")
	}
	f.serv.Do(func() { f.srv.c.update(caller, func(th *Thread) { th.Busy = false }) })
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		delivered := false
		for _, d := range f.srv.c.collaboration.Manager.View().Deliveries {
			delivered = d.State == "delivered"
		}
		if delivered {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, d := range f.srv.c.collaboration.Manager.View().Deliveries {
		if d.State != "delivered" {
			t.Fatalf("notification not delivered: %+v", d)
		}
	}
	f.serv.Do(func() {
		notices = 0
		for _, m := range f.srv.c.threadWithID(caller).Messages {
			if m.Role == "user" && strings.HasPrefix(m.Content, "[Miya collaboration]") {
				notices++
			}
		}
	})
	if notices != 1 {
		t.Fatalf("notification started %d turns", notices)
	}
}
