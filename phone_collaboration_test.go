//go:build !windows

package main

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	dg "miya/internal/delegation"
	"miya/internal/remote"
)

func TestCollaborationPhoneRequestsDoNotBlockStateStreaming(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := serverController(t.TempDir(), "")
	conn, ex := serveTestPhone(t, c)
	var from string
	ex.Do(func() {
		c.collaboration = newCollaboration(c, t.TempDir())
		c.addThread(c.cwd, Thread{ID: c.nextID(), Agent: agentCodex, ServerID: "source", HistoryLoaded: true})
		from = c.threadRef(c.threads()[0]).URL()
	})
	s := c.collaboration
	t.Cleanup(func() { s.cancel(); <-s.done })
	value, err := s.call(func() (any, error) {
		return s.manager.Create(dg.StartInput{From: from, RequestID: "fixture", Agent: agentCodex, Workspace: c.cwd, Prompt: "test"})
	})
	if err != nil {
		t.Fatal(err)
	}
	task := value.(dg.Task)
	gate, entered := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	defer release()
	s.post(func() { close(entered); <-gate })
	<-entered
	args, _ := json.Marshal(map[string]any{"request_id": "cancel", "task_id": task.ID})
	raw, _ := json.Marshal(collaborationRequest{Operation: "cancel_task", From: from, Arguments: args})
	conn.requests <- remote.Request{Op: "collaboration", RequestID: "correlation", Collaboration: raw}
	deadline := time.Now().Add(time.Second)
	for s.busy.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	ex.Do(func() { c.setStatus("State stream remains responsive") })
	streamed := false
	for !streamed {
		select {
		case f := <-conn.frames:
			if f.Type == "collaboration" {
				t.Fatal("mutation ran before its durable worker was available")
			}
			if f.Type == "state" {
				var state phoneState
				if json.Unmarshal(f.Data, &state) == nil && state.Status == "State stream remains responsive" {
					if state.Collaboration == nil || state.Collaboration.Version != 1 {
						t.Fatal("missing phone capability")
					}
					streamed = true
				}
			}
		case <-conn.ctx.Done():
			t.Fatal("durable operation blocked phone state/heartbeat loop")
		}
	}
	release()
	for {
		select {
		case f := <-conn.frames:
			if f.Type != "collaboration" {
				continue
			}
			var response phoneCollaborationResult
			if json.Unmarshal(f.Data, &response) != nil || f.Error != "" || response.RequestID != "correlation" {
				t.Fatalf("phone response: %+v", f)
			}
			if got, _ := s.manager.Read(task.ID); !got.CancelRequested {
				t.Fatal("phone did not use the shared mutation transaction")
			}
			return
		case <-conn.ctx.Done():
			t.Fatal("no collaboration response")
		}
	}
}
