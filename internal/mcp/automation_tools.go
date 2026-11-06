package mcp

import (
	"encoding/json"
	"errors"
	"fmt"

	mcpproto "miya/internal/mcp/protocol"

	"miya/internal/model"
)

var automationToolProps = map[string]any{
	"name":         map[string]any{"type": "string", "description": "Short name, in the user's language"},
	"schedule":     map[string]any{"type": "string", "description": "5-field cron in local time, e.g. 0 9 * * 1-5"},
	"instructions": map[string]any{"type": "string", "description": "First message each run sends to a coding agent: what to do, when it is done, and to reply that there is nothing to report when so"},
	"check":        map[string]any{"type": "string", "description": "Optional shell command run in the project after each round; exit 0 passes, otherwise its output starts another round"},
	"rounds":       map[string]any{"type": "integer", "minimum": 1, "maximum": 5, "description": "Most rounds when check fails (default 3; 1 without check)"},
	"agent":        map[string]any{"type": "string", "enum": []string{model.AgentCodex, model.AgentClaude}, "description": "Agent that runs it (default: this conversation's)"},
}

func init() {
	update := map[string]any{"id": map[string]any{"type": "string", "description": "Automation id from list_automations"},
		"enabled": map[string]any{"type": "boolean", "description": "Turn it on or off"}}
	for k, v := range automationToolProps {
		update[k] = v
	}
	Register(automationTool, false,
		mcpproto.Tool{Name: "list_automations", Description: "List the scheduled automations in Miya: id, name, project, schedule, whether on, and the last run.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}, Annotations: mcpproto.ReadOnly},
		mcpproto.Tool{Name: "create_automation", Description: "Create a scheduled automation in this project: on its schedule Miya starts a new conversation with the instructions. " +
			"Miya asks the user to confirm in this conversation and creates it only if they accept.",
			InputSchema: map[string]any{"type": "object", "properties": automationToolProps, "required": []string{"name", "schedule", "instructions"}},
			Annotations: map[string]any{"readOnlyHint": false, "openWorldHint": false}},
		mcpproto.Tool{Name: "update_automation", Description: "Change an automation. Turning it on or off applies at once; other changes need the user's confirmation.",
			InputSchema: map[string]any{"type": "object", "properties": update, "required": []string{"id"}},
			Annotations: map[string]any{"readOnlyHint": false, "openWorldHint": false}},
	)
}

// automationTool calls an automation tool in the service.

func automationTool(s *Server, name string, raw json.RawMessage) (string, error) {
	var args mcpproto.AutomationArgs
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &args) != nil {
		return "", errors.New("Invalid arguments")
	}
	op := map[string]string{"list_automations": "mcpAutomationList", "create_automation": "mcpAutomationCreate", "update_automation": "mcpAutomationUpdate"}[name]
	if op == "" {
		return "", fmt.Errorf("Unknown tool %s", name)
	}
	var text string
	err := s.CallService(op, mcpproto.Request{Workspace: s.Workspace(), Tool: name, Auto: &args}, &text)
	return text, err
}

// mcpAutomation answers an automation tool call in the service.

func init() {
	Register(nil, false, mcpproto.Tool{Name: "report_result",
		Description: "In a Miya automation run, report the run's structured result before finishing: a short summary, key findings, metrics, files or links produced, and whether the user needs to act. " +
			"A later call replaces an earlier one. Only automation runs use it.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"summary":  map[string]any{"type": "string", "description": "One or two sentences on what this run found or did"},
			"findings": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Key findings, one per item"},
			"metrics": map[string]any{"type": "array", "description": "Measured values to compare across runs", "items": map[string]any{"type": "object", "properties": map[string]any{
				"name":  map[string]any{"type": "string"},
				"value": map[string]any{"type": []string{"string", "number"}},
				"unit":  map[string]any{"type": "string"},
			}, "required": []string{"name", "value"}}},
			"artifacts":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Files or links produced"},
			"needs_user": map[string]any{"type": "boolean", "description": "Whether the user needs to act on this result"},
		}, "required": []string{"summary"}},
		Annotations: map[string]any{"readOnlyHint": false, "openWorldHint": false}})
}

// mcpReportResult keeps a reported result with the caller's run.
