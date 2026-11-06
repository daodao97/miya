package service

import (
	"errors"
	"fmt"

	mcpproto "miya/internal/mcp/protocol"
	"miya/internal/model"
)

func (api *Service) Handle(op string, r mcpproto.Request) (any, error) {
	c := api.host
	if op == "mcpCollaboration" {
		return api.collaboration(r)
	}
	if c.Collaboration() != nil && (op == "mcpThreads" || op == "mcpRead" || op == "mcpStatus" || op == "mcpDelegate" || op == "mcpLegacyTask") {
		if len(r.Arguments) == 0 {
			return nil, errors.New("CALLER_UNAVAILABLE: Restart the agent to use verified conversation tools")
		}
		caller, _, err := api.CallerWithArguments(r.Tool, r.Arguments)
		if err != nil {
			return nil, err
		}
		if op == "mcpThreads" {
			return api.mcpThreadsFor(caller, c.ThreadWorkspace(caller.ID)), nil
		}
		if op == "mcpDelegate" {
			return api.LegacyDelegate(r, caller)
		}
		if op == "mcpLegacyTask" {
			return api.LegacyTaskRead(r, caller)
		}
	}
	if op == "mcpRuntime" {
		return api.Runtime(r)
	}
	if r.Reference != "" {
		ref, ok := model.ParseThreadURL(r.Reference)
		if !ok || ref.Source != c.ReferenceSource() {
			return nil, errors.New("Session link is invalid or belongs to another desktop")
		}
		r.Thread = 0
		for _, t := range c.AllThreads() {
			if c.ThreadRef(t) == ref {
				r.Thread = t.ID
				break
			}
		}
		if r.Thread == 0 {
			if known, _ := c.ReferenceState(ref); known && op == "mcpRead" {
				t, messages, err := c.ResolveReference(ref)
				if err != nil {
					if _, loading := c.ReferenceState(ref); loading {
						return mcpproto.ReadResult{}, nil
					}
					return nil, err
				}
				return mcpproto.ReadResult{Loaded: true, Packet: c.ThreadPacket(t, messages)}, nil
			}
			return nil, errors.New("The conversation no longer exists or is unavailable")
		}
	}
	// The MCP server's workspace argument is not trusted: Codex runs one
	// app-server for every project, started wherever the service was. The
	// calling conversation's own project is used instead.
	switch op {
	case "mcpDesktopControl":
		return api.DesktopControl(r)
	case "mcpMemory":
		return api.Memory(r)
	case "mcpThreads":
		return api.Threads(r), nil
	case "mcpRead":
		return api.Read(r)
	case "mcpStatus":
		return api.Status(r)
	case "mcpDelegate":
		return api.Delegate(r)
	case "mcpReportResult":
		return api.reportResult(r)
	case "mcpAutomationList", "mcpAutomationCreate", "mcpAutomationUpdate":
		return api.automation(op, r)
	}
	return nil, fmt.Errorf("unknown miya call %s", op)
}

// allThreads are the conversations of every project, the open one first.
