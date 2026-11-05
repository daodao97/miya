package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"miya/internal/delegation/coordinator"
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
		return s.ResolveDelivery(r.From, r.Arguments)
	}
	if r.Operation == "cancel_all" {
		if !found {
			return nil, errors.New("THREAD_NOT_FOUND: Initiating conversation is unavailable")
		}
		return s.CancelAll(r.From, r.Arguments)
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
	a, err := coordinator.Decode(r.Operation, r.Arguments)
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
		result, err := coordinator.List(s.Manager, r.From, a, r.From == "")
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
		result := c.collaboration.TaskView(task, r.From)
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
		return c.collaboration.Operation(from, r.Operation, a)
	}
	return nil, errors.New("INVALID_ARGUMENT: Unknown collaboration operation")
}
