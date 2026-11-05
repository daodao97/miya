package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	dg "miya/internal/delegation"
)

// The authenticated desktop/phone API shares task admission and mutation with
// MCP. Its user principal may inspect/manage tasks across conversations. MCP
// tool handlers stay on the separate strict caller-evidence route.
type collaborationRequest struct {
	Operation string          `json:"operation"`
	From      string          `json:"from,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func (c *chatController) collaborationUserOp(r collaborationRequest) (any, error) {
	s := c.collaboration
	if s == nil {
		return nil, errors.New("Collaboration requires an updated Miya background service")
	}
	if s.LoadError != nil {
		return nil, fmt.Errorf("STORAGE_UNAVAILABLE: %w", s.LoadError)
	}
	caller, found := c.threadByReference(r.From)
	if r.Operation == "resolve_delivery" {
		if !found {
			return nil, errors.New("THREAD_NOT_FOUND: Receiving conversation is unavailable")
		}
		var a struct {
			RequestID  string `json:"request_id"`
			TaskID     string `json:"task_id"`
			DeliveryID string `json:"delivery_id"`
			Reason     string `json:"reason"`
		}
		d := json.NewDecoder(bytes.NewReader(r.Arguments))
		d.DisallowUnknownFields()
		if err := d.Decode(&a); err != nil {
			return nil, fmt.Errorf("INVALID_ARGUMENT: %w", err)
		}
		if d.Decode(new(any)) != io.EOF {
			return nil, errors.New("INVALID_ARGUMENT: Unexpected notification resolution arguments")
		}
		return deferred(func() (any, error) {
			return s.Call(func() (any, error) {
				receipt, fresh, err := s.Manager.Mutate(dg.Operation{Caller: r.From, RequestID: a.RequestID, Name: "abandon_delivery", TaskID: a.TaskID, DeliveryID: a.DeliveryID, Reason: a.Reason})
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
	if r.Operation == "cancel_all" {
		if !found {
			return nil, errors.New("THREAD_NOT_FOUND: Initiating conversation is unavailable")
		}
		var a struct {
			RequestID string `json:"request_id"`
		}
		d := json.NewDecoder(bytes.NewReader(r.Arguments))
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
		return deferred(func() (any, error) {
			return s.Call(func() (any, error) {
				receipt, fresh, err := s.Manager.Mutate(dg.Operation{Caller: r.From, RequestID: a.RequestID, Name: "cancel_all"})
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
	if r.Operation == "resume" {
		if !found || caller.ReadOnly {
			return nil, errors.New("THREAD_NOT_FOUND: Receiving conversation is unavailable")
		}
		return c.resumeCollaboration(r.From), nil
	}
	if len(r.Arguments) == 0 {
		r.Arguments = json.RawMessage(`{}`)
	}
	a, err := decodeCollaboration(r.Operation, r.Arguments)
	if err != nil {
		return nil, err
	}
	switch r.Operation {
	case "list_agents":
		var agents []map[string]any
		for _, st := range c.agentStatuses() {
			agents = append(agents, map[string]any{"id": st.ID, "name": st.Name, "ready": st.Ready, "models": st.ModelChoices, "efforts": st.EffortChoices})
		}
		return map[string]any{"agents": agents}, nil
	case "list_tasks":
		if r.From != "" && !found {
			return nil, errors.New("THREAD_NOT_FOUND: Initiating conversation is unavailable")
		}
		result, err := collaborationListFor(s.Manager, r.From, a, r.From == "")
		if values, ok := result.(map[string]any); ok {
			s.WithHealth(values)
			values["paused"] = c.collaboration.IsPaused(r.From) || s.Manager.Paused(r.From)
			values["revision"] = s.Manager.Revision()
		}
		return result, err
	case "read_task", "wait_task":
		task, err := s.Manager.Read(a.TaskID)
		if err != nil {
			return nil, err
		}
		result := c.collaborationTaskPublic(task, r.From)
		s.WithHealth(result)
		return result, nil
	case "start_task", "send_thread":
		if !found || caller.ReadOnly {
			return nil, errors.New("THREAD_NOT_FOUND: Initiating conversation is unavailable")
		}
		return c.startCollaboration(caller, r.Operation, a)
	case "cancel_task", "resolve_task", "watch_task", "unwatch_task":
		task, err := s.Manager.Read(a.TaskID)
		if err != nil {
			return nil, err
		}
		from := task.From
		if r.Operation == "watch_task" || r.Operation == "unwatch_task" {
			if !found {
				return nil, errors.New("THREAD_NOT_FOUND: Receiving conversation is unavailable")
			}
			from = r.From
		}
		return c.collaborationOperation(from, r.Operation, a)
	}
	return nil, errors.New("INVALID_ARGUMENT: Unknown collaboration operation")
}
