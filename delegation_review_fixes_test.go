//go:build !windows

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	dg "miya/internal/delegation"
)

func TestManagedCodexResumeFailuresNeverReturnSystemInputAsDraft(t *testing.T) {
	for _, failure := range []string{"changed", "unavailable", "rejected", "history-busy"} {
		for _, delivery := range []bool{false, true} {
			name := failure + "/task"
			if delivery {
				name = failure + "/delivery"
			}
			t.Run(name, func(t *testing.T) {
				if failure == "history-busy" {
					t.Setenv("QUICKGUI_FIXTURE_RESUME_RUNNING", "1")
				}
				if failure == "rejected" {
					t.Setenv("QUICKGUI_FIXTURE_RESUME_ERROR", `{"code":-32000,"message":"resume rejected"}`)
				}
				c, dispatch, logPath := codexResumeFixture(t)
				oldClient := c.client
				t.Cleanup(func() { oldClient.Close() })
				c.collaboration = newCollaboration(c, t.TempDir())
				s := c.collaboration
				t.Cleanup(func() { s.cancel(); <-s.done })
				c.update(1, func(th *Thread) { th.Resumed = false; th.HistoryLoaded = true; th.Draft = "user draft" })
				c.addThread(c.cwd, Thread{ID: c.nextID(), Agent: agentCodex, ServerID: "source", HistoryLoaded: true})
				from := ThreadRef{Source: c.referenceSource, Backend: agentCodex, SessionID: "source"}.URL()
				target := c.threadRef(c.threadWithID(1)).URL()
				in := dg.StartInput{From: from, RequestID: "resume", Target: target, Agent: agentCodex, Workspace: c.cwd, Prompt: "system input"}
				if delivery {
					in.From = target
					in.Target = ""
					in.Notify = true
				}
				task, err := s.manager.Create(in)
				if err != nil {
					t.Fatal(err)
				}
				id := task.ID
				if delivery {
					if err = s.manager.Observe(dg.Event{TaskID: id, State: dg.Completed}); err != nil {
						t.Fatal(err)
					}
					d, err := s.manager.ClaimDelivery(target)
					if err != nil {
						t.Fatal(err)
					}
					id = d.ID
				}
				c.managedExecutions = map[string]*managedExecution{id: {thread: 1}}
				c.managedReservations = map[int]string{1: id}
				if failure == "unavailable" {
					c.client = nil
				}
				req := SendRequest{ThreadID: 1, ExecutionID: id, Text: "system input"}
				c.resume(c.threadWithID(1), &req)
				if failure == "changed" {
					for deadline := time.Now().Add(5 * time.Second); ; {
						requests := codexTurnRequests(t, logPath)
						if len(requests) > 0 {
							break
						}
						if time.Now().After(deadline) {
							t.Fatal("resume request missing")
						}
						time.Sleep(time.Millisecond)
					}
					c.client = nil
				}
				waitCodexResume(t, c, dispatch, func() bool {
					if c.managedReservations[1] != "" {
						return false
					}
					if delivery {
						d, _ := s.manager.Delivery(id)
						return d.State == "pending"
					}
					q, _ := s.manager.Read(id)
					return q.State == dg.Failed
				})
				if c.threadWithID(1).Draft != "user draft" {
					t.Fatal("system input leaked into user draft")
				}
				if len(c.threadWithID(1).Queue) != 0 {
					t.Fatal("managed input entered ordinary queue")
				}
				if failure == "history-busy" {
					c.submitExpanded(SendRequest{ThreadID: 1, Text: "ordinary user follow-up"})
					q := c.threadWithID(1).Queue
					if len(q) != 1 || q[0].ExecutionID != "" {
						t.Fatal("ordinary user queue changed")
					}
				}
				for _, method := range codexTurnRequests(t, logPath) {
					if method == "turn/start" {
						t.Fatal("failed resume sent native input")
					}
				}
			})
		}
	}
}

func TestCollaborationInvalidArgumentsReleasePendingRequest(t *testing.T) {
	p := (&collaborationUI{}).pane("source")
	p.submit("send_thread", map[string]any{"thread": "wrong field", "task": "follow up"}, func(r collaborationRequest, done func(json.RawMessage, error)) {
		_, err := decodeCollaboration(r.Operation, r.Arguments)
		done(nil, err)
	})
	if p.pending != nil || p.busy || !strings.Contains(p.err, "INVALID_ARGUMENT:") {
		t.Fatal("invalid request left the panel locked", p.err)
	}
}

func TestCollaborationDefaultContinuationAndExplicitRecordOnly(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, agentClaude, "notify-source")
	f.serv.Do(func() { f.srv.c.update(caller, func(th *Thread) { th.Busy = true }) })
	for _, notify := range []string{"", "none"} {
		args := map[string]any{"request_id": "default-" + notify, "agent": agentClaude, "task": "review"}
		if notify != "" {
			args["notify"] = notify
		}
		first, err := collaborationMCPCall(t, f, caller, "start_task", args)
		if err != nil {
			t.Fatal(err)
		}
		if (first["notification"] == "watching") != (notify == "") {
			t.Fatal("wrong completion default", first)
		}
		q := waitCollaborationTask(t, f, first["task_id"].(string))
		retry, err := collaborationMCPCall(t, f, caller, "start_task", args)
		if err != nil || retry["task_id"] != q.ID {
			t.Fatal("retry changed notification semantics", retry, err)
		}
	}
	f.serv.Do(func() { f.srv.c.stopThread(caller) })
	paused, err := collaborationMCPCall(t, f, caller, "start_task", map[string]any{"request_id": "paused-default", "agent": agentClaude, "task": "review paused"})
	if err != nil || paused["receiving_paused"] != true || paused["notification"] != "watching" {
		t.Fatal("implicit unpause or missing pause status", paused, err)
	}
	waitCollaborationTask(t, f, paused["task_id"].(string))
	f.serv.Do(func() { f.srv.c.update(caller, func(th *Thread) { th.Busy = false }) })
}

func TestClaudeFailedRootReplyStillConfirmsNotificationAcceptedInput(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, agentClaude, "source")
	f.serv.Do(func() { f.srv.c.update(caller, func(th *Thread) { th.Busy = true }) })
	q := collaborationStoredFixtureTask(t, f, caller)
	s := f.srv.c.collaboration
	value, err := s.call(func() (any, error) {
		if _, err := s.manager.Watch(q.From, q.ID); err != nil {
			return nil, err
		}
		if err := s.manager.Observe(dg.Event{TaskID: q.ID, State: dg.Completed}); err != nil {
			return nil, err
		}
		d, err := s.manager.ClaimDelivery(q.From)
		if err != nil {
			return nil, err
		}
		return d, s.manager.BeginDeliveryInput(d.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	d := value.(dg.Delivery)
	f.serv.Do(func() {
		f.srv.c.managedEvent(d.ID, dg.Event{State: dg.Failed, NativeResultID: "bound-root-result", Result: dg.Result{Error: "model reply failed"}})
	})
	if _, err := s.call(func() (any, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	got, _ := s.manager.Delivery(d.ID)
	if got.State != "delivered" {
		t.Fatal("accepted input was confused with successful reply", got)
	}
	f.serv.Do(func() { f.srv.c.update(caller, func(th *Thread) { th.Busy = false }) })
}

func TestCollaborationDefaultChangePreservesLegacyOmittedNotifyRetry(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, agentClaude, "legacy-source")
	q := collaborationStoredFixtureTask(t, f, caller)
	result, err := collaborationMCPCall(t, f, caller, "start_task", map[string]any{"request_id": "fixture", "agent": agentClaude, "task": "fixture task"})
	if err != nil || result["task_id"] != q.ID || result["notification"] != "" {
		t.Fatal("upgrade changed already accepted request", result, err)
	}
}
