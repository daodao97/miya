package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	dg "miya/internal/delegation"
	"miya/internal/model"
	"miya/internal/textutil"
)

type collaborationArgs struct {
	RequestID      string   `json:"request_id,omitempty"`
	TaskID         string   `json:"task_id,omitempty"`
	Thread         string   `json:"thread_id,omitempty"`
	PreviousTaskID string   `json:"previous_task_id,omitempty"`
	Agent          string   `json:"agent,omitempty"`
	Title          string   `json:"title,omitempty"`
	Task           string   `json:"task,omitempty"`
	Context        string   `json:"context,omitempty"`
	Notify         string   `json:"notify,omitempty"`
	Model          string   `json:"model,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	WaitSeconds    *int     `json:"wait_seconds,omitempty"`
	Action         string   `json:"action,omitempty"`
	Reason         string   `json:"reason,omitempty"`
	Limit          int      `json:"limit,omitempty"`
	Cursor         string   `json:"cursor,omitempty"`
	State          dg.State `json:"state,omitempty"`
}

var collaborationTools = map[string]bool{"list_agents": true, "start_task": true, "read_task": true, "wait_task": true, "send_thread": true, "cancel_task": true, "resolve_task": true, "watch_task": true, "unwatch_task": true, "list_tasks": true}

func init() {
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	request := str("A unique request key for this operation. Preserve it when retrying the same request.")
	taskID := str("Exact task_id returned by start_task or send_thread.")
	for _, name := range []string{"list_agents", "start_task", "read_task", "wait_task", "send_thread", "cancel_task", "resolve_task", "watch_task", "unwatch_task", "list_tasks"} {
		props := map[string]any{}
		required := []string{}
		description := ""
		readonly := false
		switch name {
		case "list_agents":
			description = "List available agents and their native model and thinking choices."
			readonly = true
		case "start_task", "send_thread":
			description = "Submit one independent task and return immediately. By default, completion resumes this conversation so you can read the fixed result and continue the user's work. While actively waiting, use wait_task repeatedly and inspect progress; a timeout does not finish your responsibility. Use notify=none only for explicitly record-only work."
			props = map[string]any{"request_id": request, "task": str("Self-contained instructions for the target agent."), "title": str("Short display name."), "context": map[string]any{"type": "string", "enum": []string{"none", "caller_summary"}}, "notify": map[string]any{"type": "string", "enum": []string{"none", "on_finish"}, "default": "on_finish"}, "model": str("An exact model choice from list_agents."), "effort": str("An exact thinking choice from list_agents.")}
			required = []string{"request_id", "task"}
			if name == "start_task" {
				props["agent"] = str("Agent ID from list_agents; defaults to this conversation's agent.")
			} else {
				props["thread_id"] = str("Full miya:// link of an idle writable target. Busy or quota-limited targets reject instead of queueing.")
				props["previous_task_id"] = taskID
				required = append(required, "thread_id")
			}
		case "read_task", "wait_task":
			description = "Read a task's fixed result, status and latest progress without starting another execution. Later conversation turns do not change this result."
			readonly = true
			props["task_id"] = taskID
			required = []string{"task_id"}
			if name == "wait_task" {
				props["wait_seconds"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 120}
				description += " Wait at most 120 seconds (default 30). A timeout does not cancel the task."
			}
		case "cancel_task", "watch_task", "unwatch_task", "resolve_task":
			props["task_id"] = taskID
			props["request_id"] = request
			required = []string{"task_id", "request_id"}
			switch name {
			case "cancel_task":
				description = "Request cancellation of this task's exact execution, never a later turn of its target conversation. Read the task to confirm termination."
			case "watch_task":
				description = "Notify this conversation once when the task stops. The notification starts a new turn after this conversation is idle and allows collaboration continuation."
			case "unwatch_task":
				description = "Cancel an unsubmitted completion notification. Does not cancel the task."
			case "resolve_task":
				description = "Recheck an uncertain native execution, or abandon automatic tracking with a reason. Abandoning does not mark it failed or release its unknown reservation."
				props["action"] = map[string]any{"type": "string", "enum": []string{"recheck", "abandon"}}
				props["reason"] = str("Required explanation when abandoning.")
				required = append(required, "action")
			}
		case "list_tasks":
			description = "List tasks visible to this conversation, newest first. Use next_cursor unchanged for the next page."
			readonly = true
			props["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 100}
			props["cursor"] = str("Opaque next_cursor from the previous page.")
			props["state"] = map[string]any{"type": "string", "enum": []string{"queued", "starting", "running", "completed", "failed", "interrupted", "needsAttention"}}
		}
		annotations := map[string]any{"readOnlyHint": readonly, "openWorldHint": false}
		if !readonly {
			annotations["destructiveHint"] = true
		}
		mcpTools = append(mcpTools, mcpTool{Name: name, Description: description, InputSchema: map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}, Annotations: annotations})
	}
}

func decodeCollaboration(name string, raw json.RawMessage) (collaborationArgs, error) {
	var a collaborationArgs
	if !collaborationTools[name] {
		return a, errors.New("INVALID_ARGUMENT: Unknown collaboration operation")
	}
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return a, fmt.Errorf("INVALID_ARGUMENT: %w", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return a, errors.New("INVALID_ARGUMENT: Arguments must be an object")
	}
	for _, tool := range mcpTools {
		if tool.Name != name {
			continue
		}
		props := tool.InputSchema["properties"].(map[string]any)
		for key := range object {
			if _, ok := props[key]; !ok {
				return a, fmt.Errorf("INVALID_ARGUMENT: Unsupported %s argument %s", name, key)
			}
		}
		for _, key := range tool.InputSchema["required"].([]string) {
			v := object[key]
			if len(v) == 0 || string(v) == "null" || string(v) == `""` {
				return a, fmt.Errorf("INVALID_ARGUMENT: %s is required", key)
			}
		}
	}
	if a.WaitSeconds != nil && (*a.WaitSeconds < 0 || *a.WaitSeconds > 120) {
		return a, errors.New("INVALID_ARGUMENT: wait_seconds must be between 0 and 120")
	}
	if a.Limit < 0 || a.Limit > 100 {
		return a, errors.New("INVALID_ARGUMENT: limit must be between 1 and 100")
	}
	if _, explicit := object["limit"]; explicit && a.Limit == 0 {
		return a, errors.New("INVALID_ARGUMENT: limit must be between 1 and 100")
	}
	if a.RequestID != "" && (strings.TrimSpace(a.RequestID) == "" || len(a.RequestID) > 128) {
		return a, errors.New("INVALID_ARGUMENT: request_id must contain 1-128 bytes")
	}
	if a.Context != "" && a.Context != "none" && a.Context != "caller_summary" {
		return a, errors.New("INVALID_ARGUMENT: Unsupported context selection")
	}
	if a.Notify != "" && a.Notify != "none" && a.Notify != "on_finish" {
		return a, errors.New("INVALID_ARGUMENT: Unsupported notification selection")
	}
	if a.State != "" && a.State != dg.Queued && a.State != dg.Starting && a.State != dg.Running && a.State != dg.NeedsAttention && !dg.Terminal(a.State) {
		return a, errors.New("INVALID_ARGUMENT: Unknown task state")
	}
	return a, nil
}

func (s *mcpServer) collaborationTool(name string, raw json.RawMessage) (string, error) {
	a, err := decodeCollaboration(name, raw)
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
			if err := s.observerError(); err != nil {
				return "", err
			}
		}
		var result json.RawMessage
		if err := s.callService("mcpCollaboration", mcpRequest{Workspace: s.workspace, Tool: name, Arguments: raw}, &result); err != nil {
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
		if err := s.observerPause(500 * time.Millisecond); err != nil {
			return "", err
		}
	}
}

func (c *chatController) collaborationMCP(r mcpRequest) (any, error) {
	if c.collaboration == nil {
		return nil, errors.New("Collaboration requires the Miya background service")
	}
	a, err := decodeCollaboration(r.Tool, r.Arguments)
	if err != nil {
		return nil, err
	}
	caller, _, err := c.mcpCallerWithArguments(r.Tool, r.Arguments)
	if err != nil {
		return nil, err
	}
	from := c.threadRef(caller).URL()
	s := c.collaboration
	if s.loadError != nil {
		return nil, fmt.Errorf("STORAGE_UNAVAILABLE: %w", s.loadError)
	}
	if r.Tool == "list_agents" {
		var agents []map[string]any
		for _, b := range agentBackends {
			agents = append(agents, map[string]any{"id": b.ID(), "name": b.Name(), "ready": b.Ready(c), "models": b.ModelChoices(c), "efforts": b.EffortChoices(c)})
		}
		return map[string]any{"agents": agents}, nil
	}
	if r.Tool == "start_task" || r.Tool == "send_thread" {
		return c.startCollaboration(caller, r.Tool, a)
	}

	if r.Tool == "list_tasks" {
		result, err := collaborationList(s.manager, from, a)
		if values, ok := result.(map[string]any); ok {
			s.withHealth(values)
		}
		return result, err
	}
	if !s.manager.CanAccess(from, a.TaskID) {
		return nil, errors.New("ACCESS_DENIED: Task is not visible to this conversation")
	}
	switch r.Tool {
	case "read_task", "wait_task":
		task, err := s.manager.Read(a.TaskID)
		if err != nil {
			return nil, err
		}
		result := c.collaborationTaskPublic(task, from)
		s.withHealth(result)
		return result, nil
	case "cancel_task", "watch_task", "unwatch_task", "resolve_task":
		return c.collaborationOperation(from, r.Tool, a)
	}
	return nil, errors.New("Unknown collaboration operation")
}

func (c *chatController) startCollaboration(caller Thread, name string, a collaborationArgs) (any, error) {
	s := c.collaboration
	from := c.threadRef(caller).URL()
	if err := s.manager.CheckCreateRequest(from, a.RequestID); err != nil {
		return nil, err
	}
	// Retry lookup precedes mutable admission checks: the original target may
	// now be busy, gone, or use a different global default configuration.
	if previous, ok := s.manager.FindRequest(from, a.RequestID); ok {
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
			in.Notify = s.manager.RequestNotifies(in)
		}
		return deferred(func() (any, error) {
			return s.call(func() (any, error) {
				task, err := s.create(in)
				if err != nil {
					return nil, err
				}
				return c.collaborationTaskPublic(task, from), nil
			})
		}), nil
	}
	in, err := c.prepareDelegation(caller, a)
	if err != nil {
		return nil, err
	}
	return deferred(func() (any, error) {
		return s.call(func() (any, error) {
			task, err := s.create(in)
			if err != nil {
				return nil, err
			}
			return c.collaborationTaskPublic(task, from), nil
		})
	}), nil
}

// Preparing RPC work happens on the controller; durable mutations run on the
// collaboration worker. Native rechecks run outside both serial executors.
func (c *chatController) collaborationOperation(from, name string, a collaborationArgs) (any, error) {
	s := c.collaboration
	op := dg.Operation{Caller: from, RequestID: a.RequestID, Name: name, TaskID: a.TaskID, Reason: a.Reason}
	if name == "resolve_task" {
		op.Name = a.Action
		if a.Action != "abandon" && a.Action != "recheck" {
			return nil, errors.New("INVALID_ARGUMENT: Unknown resolution action")
		}
	}
	return deferred(func() (any, error) {
		var fresh bool
		result, err := s.call(func() (any, error) {
			receipt, first, err := s.manager.Mutate(op)
			fresh = first
			if err != nil {
				return nil, err
			}
			if first && name == "cancel_task" {
				task, _ := s.manager.Read(a.TaskID)
				if err := s.interruptTask(task); err != nil {
					return nil, err
				}
			}
			return map[string]any{"task_id": receipt.TaskID, "operation": receipt.Name, "accepted": receipt.Accepted, "accepted_at": receipt.AcceptedAt, "notification": receipt.Notification}, nil
		})
		if err == nil && fresh && op.Name == "recheck" {
			s.busy.Add(1)
			go func() { defer s.busy.Add(-1); s.recheckTask(from, a.TaskID) }()
		}
		return result, err
	}), nil
}

func (s *collaborationState) withHealth(result map[string]any) {
	if message := s.manager.StorageError(); message != "" {
		result["storage_error"] = message
		result["stale"] = true
	}
}

func taskPublic(task dg.Task) map[string]any {
	return map[string]any{"task_id": task.ID, "state": task.State, "thread": nullableText(task.Target), "from": task.From, "workspace": task.Workspace, "parent_task_id": task.ParentTaskID, "previous_task_id": task.PreviousTaskID, "agent": task.Agent, "title": task.Title, "revision": task.Revision, "wait_cause": task.WaitCause, "cancel_requested": task.CancelRequested, "resolution": task.Resolution, "tracking_stopped": task.Resolution == "abandoned", "progress": task.Progress, "recheck_error": task.RecheckError, "result": task.Result, "created_at": task.CreatedAt, "finished_at": task.FinishedAt}
}

func (c *chatController) collaborationTaskPublic(task dg.Task, from string) map[string]any {
	v := taskPublic(task)
	w, d, paused := c.collaboration.manager.Notification(task.ID, from)
	applyTaskNotification(v, w, d, paused)
	next := "wait_task"
	if dg.Terminal(task.State) {
		next = "read_result_and_continue"
	} else if task.State == dg.NeedsAttention {
		next = "resolve_task"
	}
	v["next_action"] = next
	return v
}
func nullableText(v string) any {
	if v == "" {
		return nil
	}
	return v
}

type taskListCursor struct {
	Created time.Time `json:"created"`
	ID      string    `json:"id"`
	Caller  string    `json:"caller"`
	State   dg.State  `json:"state"`
}

func collaborationList(manager *dg.Manager, caller string, a collaborationArgs) (any, error) {
	return collaborationListFor(manager, caller, a, false)
}

func collaborationListFor(manager *dg.Manager, caller string, a collaborationArgs, all bool) (any, error) {
	limit := a.Limit
	if limit == 0 {
		limit = 25
	}
	var cursor taskListCursor
	if a.Cursor != "" {
		data, err := base64.RawURLEncoding.DecodeString(a.Cursor)
		if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.Caller != caller || cursor.State != a.State || cursor.ID == "" {
			return nil, errors.New("INVALID_ARGUMENT: Invalid list cursor")
		}
	}
	var tasks []dg.Task
	snapshot := manager.View()
	for _, task := range snapshot.Tasks {
		if !all && !manager.CanAccess(caller, task.ID) || a.State != "" && task.State != a.State {
			continue
		}
		if a.Cursor != "" && (task.CreatedAt.After(cursor.Created) || task.CreatedAt.Equal(cursor.Created) && task.ID >= cursor.ID) {
			continue
		}
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].CreatedAt.Equal(tasks[j].CreatedAt) {
			return tasks[i].ID > tasks[j].ID
		}
		return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
	})
	var next any
	var out []map[string]any
	if len(tasks) > limit {
		last := tasks[limit-1]
		data, _ := json.Marshal(taskListCursor{last.CreatedAt, last.ID, caller, a.State})
		next = base64.RawURLEncoding.EncodeToString(data)
		tasks = tasks[:limit]
	}
	for _, task := range tasks {
		v := taskPublic(task)
		delete(v, "result")
		if dg.Terminal(task.State) {
			v["result_summary"] = textutil.ClipRunes(task.Result.Text, 280)
		}
		taskNotification(v, snapshot, task.ID, caller)
		out = append(out, v)
	}
	if out == nil {
		out = []map[string]any{}
	}
	return map[string]any{"tasks": out, "next_cursor": next}, nil
}

func taskNotification(v map[string]any, snapshot dg.Snapshot, id, from string) {
	w := snapshot.Watches[id+"\n"+from]
	for _, d := range snapshot.Deliveries {
		if d.Notify != from {
			continue
		}
		for _, taskID := range d.TaskIDs {
			if taskID == id {
				applyTaskNotification(v, w, d, snapshot.Paused[from])
				return
			}
		}
	}
	applyTaskNotification(v, w, dg.Delivery{}, snapshot.Paused[from])
}

func applyTaskNotification(v map[string]any, w dg.Watch, d dg.Delivery, paused bool) {
	v["notification"], v["receiving_paused"] = w.State, paused
	if d.ID == "" {
		return
	}
	v["delivery_id"], v["delivery_state"] = d.ID, d.State
	v["delivery_resolution"], v["delivery_reason"] = d.Resolution, d.Reason
	if d.Resolution == "abandoned" {
		v["notification"], v["delivery_reason"] = "abandoned", d.ResolutionReason
	}
}

func (c *chatController) prepareTaskRecheck(caller, id string) (any, error) {
	task, err := c.collaboration.manager.Read(id)
	if err != nil {
		return nil, err
	}
	if task.From != caller {
		return nil, errors.New("ACCESS_DENIED: Only the initiating conversation can recheck")
	}
	if task.Agent == agentClaude {
		s := c.collaboration
		return deferred(func() (any, error) {
			return s.call(func() (any, error) {
				current, err := s.manager.Read(id)
				if err != nil {
					return nil, err
				}
				if !dg.Terminal(current.State) {
					event, err := s.manager.NativeOutcome(id)
					if err != nil {
						return nil, err
					}
					if err := s.manager.Observe(event); err != nil {
						return nil, err
					}
					s.release(id)
					s.tick()
					current, _ = s.manager.Read(id)
				}
				return taskPublic(current), nil
			})
		}), nil
	}
	if task.Agent != agentCodex || task.NativeTurnID == "" || c.client == nil {
		return nil, errors.New("RECHECK_UNAVAILABLE: No verifiable native execution evidence is available")
	}
	ref, ok := model.ParseThreadURL(task.Target)
	if !ok {
		return nil, errors.New("Invalid native task identity")
	}
	client := c.client
	s := c.collaboration
	return deferred(func() (any, error) {
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
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		defer cancel()
		if err := client.Call(ctx, "thread/read", map[string]any{"threadId": ref.SessionID, "includeTurns": true}, &result); err != nil {
			return nil, err
		}
		live, liveErr := s.onMain(func() (any, error) {
			target, ok := c.threadByReference(task.Target)
			return ok && c.client == client && target.ExecutionID == task.ID && target.TurnID == task.NativeTurnID && target.Busy && task.SessionEpoch == client.RuntimeEpoch(), nil
		})
		if liveErr != nil {
			return nil, liveErr
		}
		liveBinding := live.(bool)
		return s.call(func() (any, error) {
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
					if item.Type == "agentMessage" && codexFinalPhase(item.Phase) && strings.TrimSpace(item.Text) != "" {
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
				if err := s.manager.Observe(event); err != nil {
					if dg.IsCode(err, "STALE_EVENT") {
						continue
					}
					return nil, err
				}
				matched = true
				if dg.Terminal(state) {
					s.release(id)
					s.tick()
				}
			}
			updated, _ := s.manager.Read(id)
			if !matched && !dg.Terminal(updated.State) {
				return nil, errors.New("RECHECK_UNAVAILABLE: Native history does not provide a reliable outcome for the bound execution")
			}
			return taskPublic(updated), nil
		})
	}), nil
}
