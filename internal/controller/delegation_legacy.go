package controller

import (
	"encoding/json"
	"errors"

	"miya/internal/async"
	"miya/internal/claude"
	"miya/internal/delegation/coordinator"
	"miya/internal/mcpproto"
	"miya/internal/model"
)

// Older tools keep their default wait and context choices. Their result lookup
// is still pinned to the accepted TaskID, never the target's latest turn.
func (c *Controller) LegacyDelegate(r mcpproto.Request, caller model.Thread) (any, error) {
	if c.Collaboration == nil || c.Collaboration.LoadError != nil || c.Collaboration.Manager == nil {
		return nil, errors.New("STORAGE_UNAVAILABLE: Collaboration storage is unavailable")
	}
	var raw struct {
		Task           string `json:"task"`
		Agent          string `json:"agent"`
		Thread         string `json:"thread_id"`
		IncludeContext *bool  `json:"include_context"`
	}
	if err := json.Unmarshal(r.Arguments, &raw); err != nil {
		return nil, err
	}
	a := coordinator.Args{RequestID: claude.NewSessionID(), Agent: raw.Agent, Thread: raw.Thread, Task: raw.Task, Context: "caller_summary", Notify: "none"}
	if raw.IncludeContext != nil && !*raw.IncludeContext {
		a.Context = "none"
	}
	in, err := c.prepareDelegation(caller, a)
	if err != nil {
		return nil, err
	}
	s := c.Collaboration
	return async.Reply(func() (any, error) {
		return s.Call(func() (any, error) {
			task, err := s.Create(in)
			if err != nil {
				return nil, err
			}
			return mcpproto.DelegateResult{TaskID: task.ID, Reference: task.Target, Agent: task.Agent}, nil
		})
	}), nil
}

func (c *Controller) LegacyTaskRead(r mcpproto.Request, caller model.Thread) (any, error) {
	if c.Collaboration == nil {
		return nil, errors.New("EXECUTION_ASSOCIATION_LOST: Original task identity cannot be recovered")
	}
	if c.Collaboration.LoadError != nil || c.Collaboration.Manager == nil {
		return nil, errors.New("STORAGE_UNAVAILABLE: Original task storage is unavailable")
	}
	m := c.Collaboration.Manager
	if _, err := m.Read(r.TaskID); err != nil {
		return nil, errors.New("EXECUTION_ASSOCIATION_LOST: The original execution is unavailable; inspect the target manually")
	}
	if !m.CanAccess(c.ThreadRef(caller).URL(), r.TaskID) {
		return nil, errors.New("ACCESS_DENIED: Task is not visible")
	}
	task, err := m.Read(r.TaskID)
	if err != nil {
		return nil, err
	}
	return coordinator.TaskPublic(task), nil
}
