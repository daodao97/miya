package controller

import (
	context "context"
	json "encoding/json"
	errors "errors"
	fmt "fmt"
	appmodel "miya/internal/appmodel"
	dg "miya/internal/delegation"
	coordinator "miya/internal/delegation/coordinator"
	model "miya/internal/model"
	strings "strings"
	time "time"
)

func init() {
	for _, t := range coordinator.Tools() {
		appmodel.RegisterMCPTools(collaborationTool, true, appmodel.McpTool(t))
	}
}

func collaborationTool(s *appmodel.McpServer, name string, raw json.RawMessage) (string, error) {
	a, err := coordinator.Decode(name, raw)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	start := time.Now()
	wait := 0
	if name == "wait_task" {
		wait = 30
		if a.WaitSeconds != nil {
			wait = *a.WaitSeconds
		}
	}
	for {
		if name == "wait_task" {
			if err := s.ObserverError(); err != nil {
				return "", err
			}
		}
		var result json.RawMessage
		if err := s.CallService("mcpCollaboration", appmodel.McpRequest{Workspace: s.Workspace(), Tool: name, Arguments: raw}, &result); err != nil {
			return "", err
		}
		if name != "wait_task" {
			return string(result), nil
		}
		var v struct {
			State      dg.State `json:"state"`
			Resolution string   `json:"resolution"`
		}
		if err := json.Unmarshal(result, &v); err != nil {
			return "", err
		}
		if dg.Terminal(v.State) || v.State == dg.NeedsAttention || v.Resolution == "abandoned" {
			return string(result), nil
		}
		if time.Since(start) >= time.Duration(wait)*time.Second {
			var object map[string]any
			_ = json.Unmarshal(result, &object)
			object["timed_out"] = true
			data, _ := json.Marshal(object)
			return string(data), nil
		}
		if err := s.ObserverPause(500 * time.Millisecond); err != nil {
			return "", err
		}
	}
}

func (c *Controller) collaborationMCP(r appmodel.McpRequest) (any, error) {
	if c.Collaboration == nil {
		return nil, errors.New("Collaboration requires the Miya background service")
	}
	a, err := coordinator.Decode(r.Tool, r.Arguments)
	if err != nil {
		return nil, err
	}
	caller, _, err := c.McpCallerWithArguments(r.Tool, r.Arguments)
	if err != nil {
		return nil, err
	}
	from := c.ThreadRef(caller).URL()
	s := c.Collaboration
	if s.LoadError != nil {
		return nil, fmt.Errorf("STORAGE_UNAVAILABLE: %w", s.LoadError)
	}
	if r.Tool == "list_agents" {
		return c.agentList(), nil
	}
	if r.Tool == "start_task" || r.Tool == "send_thread" {
		return c.startCollaboration(caller, r.Tool, a)
	}

	if r.Tool == "list_tasks" {
		result, err := coordinator.List(s.Manager, from, a, false)
		if values, ok := result.(map[string]any); ok {
			s.WithHealth(values)
		}
		return result, err
	}
	if !s.Manager.CanAccess(from, a.TaskID) {
		return nil, errors.New("ACCESS_DENIED: Task is not visible to this conversation")
	}
	switch r.Tool {
	case "read_task", "wait_task":
		task, err := s.Manager.Read(a.TaskID)
		if err != nil {
			return nil, err
		}
		result := c.Collaboration.TaskView(task, from)
		s.WithHealth(result)
		return result, nil
	case "cancel_task", "watch_task", "unwatch_task", "resolve_task":
		return c.Collaboration.Operation(from, r.Tool, a)
	}
	return nil, errors.New("Unknown collaboration operation")
}

func (c *Controller) startCollaboration(caller appmodel.Thread, name string, a coordinator.Args) (any, error) {
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
		return appmodel.Deferred(func() (any, error) {
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
	return appmodel.Deferred(func() (any, error) {
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
	return appmodel.Deferred(func() (any, error) {
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
					if item.Type == "agentMessage" && appmodel.CodexFinalPhase(item.Phase) && strings.TrimSpace(item.Text) != "" {
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
