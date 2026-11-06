//go:build !windows

package main

import (
	json "encoding/json"
	appmodel "miya/internal/appmodel"
	controller "miya/internal/controller"
	dg "miya/internal/delegation"
	coordinator "miya/internal/delegation/coordinator"
	widgets "miya/internal/widgets"
	strings "strings"
	testing "testing"
	time "time"
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
				oldClient := c.Client
				t.Cleanup(func() { oldClient.Close() })
				c.Collaboration = controller.NewCollaboration(c, t.TempDir())
				s := c.Collaboration
				t.Cleanup(func() { s.Stop(); <-s.Done() })
				c.Update(1, func(th *appmodel.Thread) { th.Resumed = false; th.HistoryLoaded = true; th.Draft = "user draft" })
				c.AddThread(c.Cwd, appmodel.Thread{ID: c.NextID(), Agent: appmodel.AgentCodex, ServerID: "source", HistoryLoaded: true})
				from := appmodel.ThreadRef{Source: c.ReferenceSource, Backend: appmodel.AgentCodex, SessionID: "source"}.URL()
				target := c.ThreadRef(c.ThreadWithID(1)).URL()
				in := dg.StartInput{From: from, RequestID: "resume", Target: target, Agent: appmodel.AgentCodex, Workspace: c.Cwd, Prompt: "system input"}
				if delivery {
					in.From = target
					in.Target = ""
					in.Notify = true
				}
				task, err := s.Manager.Create(in)
				if err != nil {
					t.Fatal(err)
				}
				id := task.ID
				if delivery {
					if err = s.Manager.Observe(dg.Event{TaskID: id, State: dg.Completed}); err != nil {
						t.Fatal(err)
					}
					d, err := s.Manager.ClaimDelivery(target)
					if err != nil {
						t.Fatal(err)
					}
					id = d.ID
				}
				c.Collaboration.Executions = map[string]*controller.ManagedExecution{id: {Thread: 1}}
				c.Collaboration.Reservations = map[int]string{1: id}
				if failure == "unavailable" {
					c.Client = nil
				}
				req := appmodel.SendRequest{ThreadID: 1, ExecutionID: id, Text: "system input"}
				c.Resume(c.ThreadWithID(1), &req)
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
					c.Client = nil
				}
				waitCodexResume(t, c, dispatch, func() bool {
					if c.Collaboration.Reservations[1] != "" {
						return false
					}
					if delivery {
						d, _ := s.Manager.Delivery(id)
						return d.State == "pending"
					}
					q, _ := s.Manager.Read(id)
					return q.State == dg.Failed
				})
				if c.ThreadWithID(1).Draft != "user draft" {
					t.Fatal("system input leaked into user draft")
				}
				if len(c.ThreadWithID(1).Queue) != 0 {
					t.Fatal("managed input entered ordinary queue")
				}
				if failure == "history-busy" {
					c.SubmitExpanded(appmodel.SendRequest{ThreadID: 1, Text: "ordinary user follow-up"})
					q := c.ThreadWithID(1).Queue
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
	p := (&widgets.CollaborationUI{}).Pane("source")
	p.Submit("send_thread", map[string]any{"thread": "wrong field", "task": "follow up"}, func(r appmodel.CollaborationRequest, done func(json.RawMessage, error)) {
		_, err := coordinator.Decode(r.Operation, r.Arguments)
		done(nil, err)
	})
	if p.Pending != nil || p.Busy || !strings.Contains(p.Err, "INVALID_ARGUMENT:") {
		t.Fatal("invalid request left the panel locked", p.Err)
	}
}

func TestCollaborationDefaultContinuationAndExplicitRecordOnly(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, appmodel.AgentClaude, "notify-source")
	f.serv.Do(func() { f.srv.C.Update(caller, func(th *appmodel.Thread) { th.Busy = true }) })
	for _, notify := range []string{"", "none"} {
		args := map[string]any{"request_id": "default-" + notify, "agent": appmodel.AgentClaude, "task": "review"}
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
	f.serv.Do(func() { f.srv.C.StopThread(caller) })
	paused, err := collaborationMCPCall(t, f, caller, "start_task", map[string]any{"request_id": "paused-default", "agent": appmodel.AgentClaude, "task": "review paused"})
	if err != nil || paused["receiving_paused"] != true || paused["notification"] != "watching" {
		t.Fatal("implicit unpause or missing pause status", paused, err)
	}
	waitCollaborationTask(t, f, paused["task_id"].(string))
	f.serv.Do(func() { f.srv.C.Update(caller, func(th *appmodel.Thread) { th.Busy = false }) })
}

func TestClaudeFailedRootReplyStillConfirmsNotificationAcceptedInput(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, appmodel.AgentClaude, "source")
	f.serv.Do(func() { f.srv.C.Update(caller, func(th *appmodel.Thread) { th.Busy = true }) })
	q := collaborationStoredFixtureTask(t, f, caller)
	s := f.srv.C.Collaboration
	value, err := s.Call(func() (any, error) {
		if _, err := s.Manager.Watch(q.From, q.ID); err != nil {
			return nil, err
		}
		if err := s.Manager.Observe(dg.Event{TaskID: q.ID, State: dg.Completed}); err != nil {
			return nil, err
		}
		d, err := s.Manager.ClaimDelivery(q.From)
		if err != nil {
			return nil, err
		}
		return d, s.Manager.BeginDeliveryInput(d.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	d := value.(dg.Delivery)
	f.serv.Do(func() {
		f.srv.C.ManagedEvent(d.ID, dg.Event{State: dg.Failed, NativeResultID: "bound-root-result", Result: dg.Result{Error: "model reply failed"}})
	})
	if _, err := s.Call(func() (any, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Manager.Delivery(d.ID)
	if got.State != "delivered" {
		t.Fatal("accepted input was confused with successful reply", got)
	}
	f.serv.Do(func() { f.srv.C.Update(caller, func(th *appmodel.Thread) { th.Busy = false }) })
}

func TestCollaborationDefaultChangePreservesLegacyOmittedNotifyRetry(t *testing.T) {
	f := newServiceFixture(t)
	caller := collaborationCaller(t, f, appmodel.AgentClaude, "legacy-source")
	q := collaborationStoredFixtureTask(t, f, caller)
	result, err := collaborationMCPCall(t, f, caller, "start_task", map[string]any{"request_id": "fixture", "agent": appmodel.AgentClaude, "task": "fixture task"})
	if err != nil || result["task_id"] != q.ID || result["notification"] != "" {
		t.Fatal("upgrade changed already accepted request", result, err)
	}
}
