package mcp

import (
	"encoding/json"
	"errors"
	"fmt"

	"miya/internal/automation"
	"miya/internal/chat/model"
	mcpproto "miya/internal/mcp/protocol"
)

var automationToolProps = map[string]any{
	"name":     map[string]any{"type": "string", "description": "Short name, in the user's language"},
	"schedule": map[string]any{"type": "string", "description": "5-field cron in local time, e.g. 0 9 * * 1-5. Leave empty when only triggers wake it"},
	"triggers": map[string]any{"type": "array", "description": "Other ways it wakes, any of which starts a run. " +
		"condition: a shell command Miya runs in the project. Miya does not know what it looks at, so thresholds and comparisons belong in the command. Give a recipe, which is a ready-made command with params, or a command of your own. Recipes (the .json files in " + automation.RecipeDir() + "): " + automation.RecipeHints() + ". E.g. {kind:condition, condition:{recipe:disk_free, params:{VOLUME:\"/\", BELOW_GB:\"5\"}}} for free disk space under 5 GB. " +
		"A command of your own wakes as when says. success (default): run every everyMinutes (default 5), met when it exits 0, not met when it exits 1, any other status is an error, and the first line it prints is shown; it wakes when it turns met and again after repeatHours (default 24) while still met. changed: run every everyMinutes, it wakes each time the command prints something else than before and gives the run what it printed, e.g. {command:\"gh issue list --limit 20\", when:changed}. line: Miya keeps the command running and wakes with each line it prints, for a command that waits for events, e.g. {command:\"tail -n 0 -F app.log | grep --line-buffered ERROR\", when:line}; set policy queue with it so that lines printed during a run are kept. Keep success and changed commands quick and free of side effects. " +
		"ingress: woken from outside by `miya -wake <id>` or a local HTTP request with a token. file: when a file or a directory's entries change (path). " +
		"after: when another automation's run finishes (after: its id; results: passed/failed/interrupted). system: event wake, when the computer wakes from sleep.",
		"items": map[string]any{"type": "object", "required": []string{"kind"}, "properties": map[string]any{
			"kind": map[string]any{"type": "string", "enum": []string{"condition", "ingress", "file", "after", "system"}},
			"condition": map[string]any{"type": "object", "properties": map[string]any{
				"recipe":       map[string]any{"type": "string"},
				"params":       map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				"command":      map[string]any{"type": "string"},
				"when":         map[string]any{"type": "string", "enum": []string{automation.WhenSuccess, automation.WhenChanged, automation.WhenLine}},
				"everyMinutes": map[string]any{"type": "integer"},
				"repeatHours":  map[string]any{"type": "integer"}}},
			"path":    map[string]any{"type": "string"},
			"after":   map[string]any{"type": "string"},
			"results": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"event":   map[string]any{"type": "string", "enum": []string{"wake"}}}}},
	"policy": map[string]any{"type": "object", "description": "Holds back triggered wakes: queue (keep one wake that comes while running), debounceSeconds, cooldownMinutes, maxPerDay (default 24), gate (a condition as in triggers, of a recipe or a success command, checked on every wake; the run starts only when met)",
		"properties": map[string]any{"queue": map[string]any{"type": "boolean"}, "debounceSeconds": map[string]any{"type": "integer"},
			"cooldownMinutes": map[string]any{"type": "integer"}, "maxPerDay": map[string]any{"type": "integer"}, "gate": map[string]any{"type": "object"}}},
	"instructions": map[string]any{"type": "string", "description": "First message each run sends to a coding agent: what to do, when it is done, and to reply that there is nothing to report when so"},
	"check":        map[string]any{"type": "string", "description": "Optional shell command run in the project after each round; exit 0 passes, otherwise its output starts another round"},
	"rounds":       map[string]any{"type": "integer", "minimum": 1, "maximum": 5, "description": "Most rounds when check fails (default 3; 1 without check)"},
	"agent":        map[string]any{"type": "string", "enum": model.Backends, "description": "Agent that runs it (default: this conversation's)"},
	"worktree":     map[string]any{"type": "boolean", "description": "Run each run in a separate Git worktree on its own branch, so it cannot collide with edits in the project; the user merges or discards its changes afterwards. Use for automations that change code"},
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
		mcpproto.Tool{Name: "create_automation", Description: "Create an automation in this project: on its schedule, or when one of its triggers fires (a condition such as low disk space, an external wake, a file change, another automation finishing, the computer waking), Miya starts a new conversation with the instructions. " +
			"Give a schedule, triggers, or both. Miya asks the user to confirm in this conversation and creates it only if they accept.",
			InputSchema: map[string]any{"type": "object", "properties": automationToolProps, "required": []string{"name", "instructions"}},
			Annotations: map[string]any{"readOnlyHint": false, "openWorldHint": false}},
		mcpproto.Tool{Name: "wake_automation", Description: "Start a run of an automation now, as if one of its triggers fired; data goes to its agent as untrusted context. " +
			"Use it to chain work into an existing automation. Miya asks the user to confirm in this conversation first.",
			InputSchema: map[string]any{"type": "object", "required": []string{"id"}, "properties": map[string]any{
				"id":   map[string]any{"type": "string", "description": "Automation id or name from list_automations"},
				"data": map[string]any{"type": "string", "description": "Optional context for the run, up to 4000 characters"}}},
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
	op := map[string]string{"list_automations": "mcpAutomationList", "create_automation": "mcpAutomationCreate", "update_automation": "mcpAutomationUpdate", "wake_automation": "mcpAutomationWake"}[name]
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
