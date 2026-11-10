package coordinator

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"miya/internal/async"
	dg "miya/internal/delegation"
	"miya/internal/textutil"
)

// Command describes the shared collaboration API used by UI and tool adapters.
type Command struct {
	Name, Description string
	Schema            map[string]any
	ReadOnly          bool
}

var commands []Command

func Commands() []Command        { return commands }
func IsCommand(name string) bool { return commandNames[name] }

type Args struct {
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

var commandNames = map[string]bool{"list_agents": true, "start_task": true, "read_task": true, "wait_task": true, "send_thread": true, "cancel_task": true, "resolve_task": true, "watch_task": true, "unwatch_task": true, "list_tasks": true}

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
		commands = append(commands, Command{Name: name, Description: description, Schema: map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}, ReadOnly: readonly})
	}
}

// Decode checks a collaboration command's arguments against its shared schema.
func Decode(name string, raw json.RawMessage) (Args, error) {
	var a Args
	if !commandNames[name] {
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
	for _, command := range commands {
		if command.Name != name {
			continue
		}
		props := command.Schema["properties"].(map[string]any)
		for key := range object {
			if _, ok := props[key]; !ok {
				return a, fmt.Errorf("INVALID_ARGUMENT: Unsupported %s argument %s", name, key)
			}
		}
		for _, key := range command.Schema["required"].([]string) {
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

// Preparing RPC work happens on the controller; durable mutations run on the
// collaboration worker. Native rechecks run outside both serial executors.
func (s *Coordinator) Operation(from, name string, a Args) (any, error) {
	op := dg.Operation{Caller: from, RequestID: a.RequestID, Name: name, TaskID: a.TaskID, Reason: a.Reason}
	if name == "resolve_task" {
		op.Name = a.Action
		if a.Action != "abandon" && a.Action != "recheck" {
			return nil, errors.New("INVALID_ARGUMENT: Unknown resolution action")
		}
	}
	return async.Reply(func() (any, error) {
		var fresh bool
		result, err := s.Call(func() (any, error) {
			receipt, first, err := s.Manager.Mutate(op)
			fresh = first
			if err != nil {
				return nil, err
			}
			if first && name == "cancel_task" {
				task, _ := s.Manager.Read(a.TaskID)
				if err := s.InterruptTask(task); err != nil {
					return nil, err
				}
			}
			return map[string]any{"task_id": receipt.TaskID, "operation": receipt.Name, "accepted": receipt.Accepted, "accepted_at": receipt.AcceptedAt, "notification": receipt.Notification}, nil
		})
		if err == nil && fresh && op.Name == "recheck" {
			s.StartRecheck(from, a.TaskID)
		}
		return result, err
	}), nil
}

// TaskView is a task as the conversation from sees it: its notification
// and what to do next.
func (s *Coordinator) TaskView(task dg.Task, from string) map[string]any {
	v := TaskPublic(task)
	w, d, paused := s.Manager.Notification(task.ID, from)
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

type taskListCursor struct {
	Created time.Time `json:"created"`
	ID      string    `json:"id"`
	Caller  string    `json:"caller"`
	State   dg.State  `json:"state"`
}

// List is a page of the tasks caller may see, or of all of them.
func List(manager *dg.Manager, caller string, a Args, all bool) (any, error) {
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
		v := TaskPublic(task)
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
	if d.Resolution == dg.ResolutionAbandoned {
		v["notification"], v["delivery_reason"] = dg.ResolutionAbandoned, d.ResolutionReason
	}
}

// RecheckRecorded settles a task from the native outcome recorded for it,
// when it has not ended yet.
func (s *Coordinator) RecheckRecorded(id string) async.Reply {
	return async.Reply(func() (any, error) {
		return s.Call(func() (any, error) {
			current, err := s.Manager.Read(id)
			if err != nil {
				return nil, err
			}
			if !dg.Terminal(current.State) {
				event, err := s.Manager.NativeOutcome(id)
				if err != nil {
					return nil, err
				}
				if err := s.Manager.Observe(event); err != nil {
					return nil, err
				}
				s.Release(id)
				s.Tick()
				current, _ = s.Manager.Read(id)
			}
			return TaskPublic(current), nil
		})
	})
}

// ResolveDelivery abandons a completion notice to receiver, as the person
// asked.
func (s *Coordinator) ResolveDelivery(receiver string, raw json.RawMessage) (any, error) {
	var a struct {
		RequestID  string `json:"request_id"`
		TaskID     string `json:"task_id"`
		DeliveryID string `json:"delivery_id"`
		Reason     string `json:"reason"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return nil, fmt.Errorf("INVALID_ARGUMENT: %w", err)
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, errors.New("INVALID_ARGUMENT: Unexpected notification resolution arguments")
	}
	return async.Reply(func() (any, error) {
		return s.Call(func() (any, error) {
			receipt, fresh, err := s.Manager.Mutate(dg.Operation{Caller: receiver, RequestID: a.RequestID, Name: "abandon_delivery", TaskID: a.TaskID, DeliveryID: a.DeliveryID, Reason: a.Reason})
			if err != nil {
				return nil, err
			}
			if fresh {
				s.Release(a.DeliveryID)
				s.Tick()
			}
			return map[string]any{"accepted": receipt.Accepted, "task_id": receipt.TaskID, "notification": receipt.Notification}, nil
		})
	}), nil
}

// CancelAll cancels every task caller started, as the person asked.
func (s *Coordinator) CancelAll(caller string, raw json.RawMessage) (any, error) {
	var a struct {
		RequestID string `json:"request_id"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return nil, fmt.Errorf("INVALID_ARGUMENT: %w", err)
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, errors.New("INVALID_ARGUMENT: Unexpected cancellation arguments")
	}
	if a.RequestID == "" {
		return nil, errors.New("INVALID_ARGUMENT: request_id is required")
	}
	return async.Reply(func() (any, error) {
		return s.Call(func() (any, error) {
			receipt, fresh, err := s.Manager.Mutate(dg.Operation{Caller: caller, RequestID: a.RequestID, Name: "cancel_all"})
			if err != nil {
				return nil, err
			}
			if fresh {
				for _, id := range receipt.TaskIDs {
					if task, err := s.Manager.Read(id); err == nil {
						if err := s.InterruptTask(task); err != nil {
							return nil, err
						}
					}
				}
			}
			return map[string]any{"accepted": true, "task_ids": receipt.TaskIDs}, nil
		})
	}), nil
}
