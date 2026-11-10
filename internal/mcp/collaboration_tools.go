package mcp

import (
	"encoding/json"
	"time"

	dg "miya/internal/delegation"
	"miya/internal/delegation/coordinator"
	mcpproto "miya/internal/mcp/protocol"
)

func init() {
	for _, command := range coordinator.Commands() {
		annotations := map[string]any{"readOnlyHint": command.ReadOnly, "openWorldHint": false}
		if !command.ReadOnly {
			annotations["destructiveHint"] = true
		}
		Register(collaborationTool, true, mcpproto.Tool{Name: command.Name, Description: command.Description, InputSchema: command.Schema, Annotations: annotations})
	}
}

func collaborationTool(s *Server, name string, raw json.RawMessage) (string, error) {
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
		if err := s.CallService("mcpCollaboration", mcpproto.Request{Workspace: s.Workspace(), Tool: name, Arguments: raw}, &result); err != nil {
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
		if dg.Terminal(v.State) || v.State == dg.NeedsAttention || v.Resolution == dg.ResolutionAbandoned {
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
