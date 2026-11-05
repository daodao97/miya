package main

import (
	"encoding/json"
	"errors"
	"time"

	"miya/internal/claude"
	dg "miya/internal/delegation"
	"miya/internal/delegation/coordinator"
)

// Older tools keep their default wait and context choices. Their result lookup
// is still pinned to the accepted TaskID, never the target's latest turn.
func (c *chatController) legacyDelegate(r mcpRequest, caller Thread) (any, error) {
	if c.collaboration == nil || c.collaboration.LoadError != nil || c.collaboration.Manager == nil {
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
	s := c.collaboration
	return deferred(func() (any, error) {
		return s.Call(func() (any, error) {
			task, err := s.Create(in)
			if err != nil {
				return nil, err
			}
			return mcpDelegateResult{TaskID: task.ID, Reference: task.Target, Agent: task.Agent}, nil
		})
	}), nil
}

func (c *chatController) legacyTaskRead(r mcpRequest, caller Thread) (any, error) {
	if c.collaboration == nil {
		return nil, errors.New("EXECUTION_ASSOCIATION_LOST: Original task identity cannot be recovered")
	}
	if c.collaboration.LoadError != nil || c.collaboration.Manager == nil {
		return nil, errors.New("STORAGE_UNAVAILABLE: Original task storage is unavailable")
	}
	m := c.collaboration.Manager
	if _, err := m.Read(r.TaskID); err != nil {
		return nil, errors.New("EXECUTION_ASSOCIATION_LOST: The original execution is unavailable; inspect the target manually")
	}
	if !m.CanAccess(c.threadRef(caller).URL(), r.TaskID) {
		return nil, errors.New("ACCESS_DENIED: Task is not visible")
	}
	task, err := m.Read(r.TaskID)
	if err != nil {
		return nil, err
	}
	return coordinator.TaskPublic(task), nil
}

func legacyTaskStatus(s *mcpServer, req mcpRequest, wait time.Duration) (string, error) {
	deadline := time.Now().Add(wait)
	for {
		if err := s.ObserverError(); err != nil {
			return "", err
		}
		var result json.RawMessage
		if err := s.CallService("mcpLegacyTask", req, &result); err != nil {
			return "", err
		}
		var v struct {
			State      dg.State `json:"state"`
			Resolution string   `json:"resolution"`
		}
		if err := json.Unmarshal(result, &v); err != nil {
			return "", err
		}
		if dg.Terminal(v.State) || v.Resolution == "abandoned" {
			return string(result), nil
		}
		if !time.Now().Before(deadline) {
			var value map[string]any
			_ = json.Unmarshal(result, &value)
			value["timed_out"] = true
			data, _ := json.Marshal(value)
			return string(data), nil
		}
		if err := s.ObserverPause(500 * time.Millisecond); err != nil {
			return "", err
		}
	}
}
