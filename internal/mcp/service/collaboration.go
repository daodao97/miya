package service

import (
	"errors"
	"fmt"

	"miya/internal/delegation/coordinator"
	mcpproto "miya/internal/mcp/protocol"
)

func (api *Service) collaboration(r mcpproto.Request) (any, error) {
	c := api.host
	if c.Collaboration() == nil {
		return nil, errors.New("Collaboration requires the Miya background service")
	}
	a, err := coordinator.Decode(r.Tool, r.Arguments)
	if err != nil {
		return nil, err
	}
	caller, _, err := api.CallerWithArguments(r.Tool, r.Arguments)
	if err != nil {
		return nil, err
	}
	from := c.ThreadRef(caller).URL()
	s := c.Collaboration()
	if s.LoadError != nil {
		return nil, fmt.Errorf("STORAGE_UNAVAILABLE: %w", s.LoadError)
	}
	if r.Tool == "list_agents" {
		return c.AgentList(), nil
	}
	if r.Tool == "start_task" || r.Tool == "send_thread" {
		return c.StartCollaboration(caller, r.Tool, a)
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
		result := c.Collaboration().TaskView(task, from)
		s.WithHealth(result)
		return result, nil
	case "cancel_task", "watch_task", "unwatch_task", "resolve_task":
		return c.Collaboration().Operation(from, r.Tool, a)
	}
	return nil, errors.New("Unknown collaboration operation")
}
