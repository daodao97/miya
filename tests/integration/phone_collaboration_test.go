//go:build !windows

package integration

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/controller"
	dg "miya/internal/delegation"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
)

func TestCollaborationPhoneRequestsDoNotBlockStateStreaming(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := serverController(t.TempDir(), "")
	conn, ex := serveTestPhone(t, c)
	var from string
	ex.Do(func() {
		c.Collaboration = controller.NewCollaboration(c, t.TempDir())
		c.AddThread(c.Cwd, model.Thread{ID: c.NextID(), Agent: appmodel.AgentCodex, ServerID: "source", HistoryLoaded: true})
		from = c.ThreadRef(c.Threads()[0]).URL()
	})
	s := c.Collaboration
	t.Cleanup(func() { s.Stop(); <-s.Done() })
	value, err := s.Call(func() (any, error) {
		return s.Manager.Create(dg.StartInput{From: from, RequestID: "fixture", Agent: appmodel.AgentCodex, Workspace: c.Cwd, Prompt: "test"})
	})
	if err != nil {
		t.Fatal(err)
	}
	task := value.(dg.Task)
	gate, entered := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	defer release()
	s.Post(func() { close(entered); <-gate })
	<-entered
	args, _ := json.Marshal(map[string]any{"request_id": "cancel", "task_id": task.ID})
	raw, _ := json.Marshal(appmodel.CollaborationRequest{Operation: "cancel_task", From: from, Arguments: args})
	conn.requests <- remote.Request{Op: "collaboration", RequestID: "correlation", Collaboration: raw}
	deadline := time.Now().Add(time.Second)
	for s.Pending() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	ex.Do(func() { c.SetStatus("State stream remains responsive") })
	streamed := false
	for !streamed {
		select {
		case f := <-conn.frames:
			if f.Type == "collaboration" {
				t.Fatal("mutation ran before its durable worker was available")
			}
			if f.Type == "state" {
				var state phoneproto.State
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
			var response phoneproto.CollaborationResult
			if json.Unmarshal(f.Data, &response) != nil || f.Error != "" || response.RequestID != "correlation" {
				t.Fatalf("phone response: %+v", f)
			}
			if got, _ := s.Manager.Read(task.ID); !got.CancelRequested {
				t.Fatal("phone did not use the shared mutation transaction")
			}
			return
		case <-conn.ctx.Done():
			t.Fatal("no collaboration response")
		}
	}
}
