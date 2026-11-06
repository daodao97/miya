package controller

import (
	"context"
	"errors"
	"strings"
	"time"

	"miya/internal/appmodel"
	"miya/internal/async"
	dg "miya/internal/delegation"
	"miya/internal/delegation/coordinator"
	"miya/internal/model"
)

func (c *Controller) startCollaboration(caller model.Thread, name string, a coordinator.Args) (any, error) {
	s := c.Collaboration
	from := c.ThreadRef(caller).URL()
	if err := s.Manager.CheckCreateRequest(from, a.RequestID); err != nil {
		return nil, err
	}
	// Retry lookup precedes mutable admission checks: the original target may
	// now be busy, gone, or use a different global default configuration.
	if previous, ok := s.Manager.FindRequest(from, a.RequestID); ok {
		agent := a.Agent
		if agent == "" {
			agent = model.ThreadAgent(caller)
		}
		if name == "send_thread" {
			agent = previous.Agent
		}
		in := dg.StartInput{From: from, RequestID: a.RequestID, Target: a.Thread, ParentTaskID: previous.ParentTaskID, PreviousTaskID: a.PreviousTaskID, Agent: model.NormalizeAgentID(agent), Workspace: previous.Workspace, Title: a.Title, Prompt: a.Task, Context: a.Context, Snapshot: previous.Snapshot, Requested: dg.Config{Model: a.Model, Effort: a.Effort}, Effective: previous.Config, Notify: a.Notify == "on_finish"}
		// An omitted default belongs to the first acceptance, including tasks
		// accepted before automatic completion became the default.
		if a.Notify == "" {
			in.Notify = s.Manager.RequestNotifies(in)
		}
		return async.Reply(func() (any, error) {
			return s.Call(func() (any, error) {
				task, err := s.Create(in)
				if err != nil {
					return nil, err
				}
				return c.Collaboration.TaskView(task, from), nil
			})
		}), nil
	}
	in, err := c.prepareDelegation(caller, a)
	if err != nil {
		return nil, err
	}
	return async.Reply(func() (any, error) {
		return s.Call(func() (any, error) {
			task, err := s.Create(in)
			if err != nil {
				return nil, err
			}
			return c.Collaboration.TaskView(task, from), nil
		})
	}), nil
}

func (c *Controller) PrepareTaskRecheck(caller, id string) (any, error) {
	task, err := c.Collaboration.Manager.Read(id)
	if err != nil {
		return nil, err
	}
	if task.From != caller {
		return nil, errors.New("ACCESS_DENIED: Only the initiating conversation can recheck")
	}
	if task.Agent == appmodel.AgentClaude {
		return c.Collaboration.RecheckRecorded(id), nil
	}
	if task.Agent != appmodel.AgentCodex || task.NativeTurnID == "" || c.Client == nil {
		return nil, errors.New("RECHECK_UNAVAILABLE: No verifiable native execution evidence is available")
	}
	ref, ok := model.ParseThreadURL(task.Target)
	if !ok {
		return nil, errors.New("Invalid native task identity")
	}
	client := c.Client
	s := c.Collaboration
	return async.Reply(func() (any, error) {
		var result struct {
			Thread struct {
				Turns []struct {
					ID     string
					Status string
					Items  []struct{ Type, Text, Phase string }
					Error  *struct{ Message string }
				}
			}
		}
		ctx, cancel := context.WithTimeout(s.Context(), 10*time.Second)
		defer cancel()
		if err := client.Call(ctx, "thread/read", map[string]any{"threadId": ref.SessionID, "includeTurns": true}, &result); err != nil {
			return nil, err
		}
		live, liveErr := s.OnMain(func() (any, error) {
			target, ok := c.ThreadByReference(task.Target)
			return ok && c.Client == client && target.ExecutionID == task.ID && target.TurnID == task.NativeTurnID && target.Busy && task.SessionEpoch == client.RuntimeEpoch(), nil
		})
		if liveErr != nil {
			return nil, liveErr
		}
		liveBinding := live.(bool)
		return s.Call(func() (any, error) {
			matched := false
			for _, turn := range result.Thread.Turns {
				if turn.ID != task.NativeTurnID {
					continue
				}
				state := dg.State(turn.Status)
				if state == "inProgress" {
					if !liveBinding {
						continue
					}
					state = dg.Running
				}
				if state != dg.Running && !dg.Terminal(state) {
					continue
				}
				r := dg.Result{}
				for _, item := range turn.Items {
					if item.Type == "agentMessage" && coordinator.CodexFinalPhase(item.Phase) && strings.TrimSpace(item.Text) != "" {
						r.Text = item.Text
					}
				}
				if turn.Error != nil {
					r.Error = turn.Error.Message
				}
				event := dg.Event{TaskID: id, Target: task.Target, NativeTurnID: turn.ID, SessionEpoch: task.SessionEpoch, State: state, Result: r}
				if state == dg.Running {
					event.ExpectedRevision = task.Revision
				}
				if err := s.Manager.Observe(event); err != nil {
					if dg.IsCode(err, "STALE_EVENT") {
						continue
					}
					return nil, err
				}
				matched = true
				if dg.Terminal(state) {
					s.Release(id)
					s.Tick()
				}
			}
			updated, _ := s.Manager.Read(id)
			if !matched && !dg.Terminal(updated.State) {
				return nil, errors.New("RECHECK_UNAVAILABLE: Native history does not provide a reliable outcome for the bound execution")
			}
			return coordinator.TaskPublic(updated), nil
		})
	}), nil
}
