package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"miya/internal/automation"
	"miya/internal/model"
	"miya/internal/textutil"
)

// The miya MCP tools for automations: an agent turns what the person said
// into settings and calls create_automation; Miya shows a confirmation card
// in that conversation and creates the automation only if they accept.

var automationToolProps = map[string]any{
	"name":         map[string]any{"type": "string", "description": "Short name, in the user's language"},
	"schedule":     map[string]any{"type": "string", "description": "5-field cron in local time, e.g. 0 9 * * 1-5"},
	"instructions": map[string]any{"type": "string", "description": "First message each run sends to a coding agent: what to do, when it is done, and to reply that there is nothing to report when so"},
	"check":        map[string]any{"type": "string", "description": "Optional shell command run in the project after each round; exit 0 passes, otherwise its output starts another round"},
	"rounds":       map[string]any{"type": "integer", "minimum": 1, "maximum": 5, "description": "Most rounds when check fails (default 3; 1 without check)"},
	"agent":        map[string]any{"type": "string", "enum": []string{agentCodex, agentClaude}, "description": "Agent that runs it (default: this conversation's)"},
}

func init() {
	update := map[string]any{"id": map[string]any{"type": "string", "description": "Automation id from list_automations"},
		"enabled": map[string]any{"type": "boolean", "description": "Turn it on or off"}}
	for k, v := range automationToolProps {
		update[k] = v
	}
	registerMCPTools(automationTool, false,
		mcpTool{Name: "list_automations", Description: "List the scheduled automations in Miya: id, name, project, schedule, whether on, and the last run.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}, Annotations: mcpReadOnly},
		mcpTool{Name: "create_automation", Description: "Create a scheduled automation in this project: on its schedule Miya starts a new conversation with the instructions. " +
			"Miya asks the user to confirm in this conversation and creates it only if they accept.",
			InputSchema: map[string]any{"type": "object", "properties": automationToolProps, "required": []string{"name", "schedule", "instructions"}},
			Annotations: map[string]any{"readOnlyHint": false, "openWorldHint": false}},
		mcpTool{Name: "update_automation", Description: "Change an automation. Turning it on or off applies at once; other changes need the user's confirmation.",
			InputSchema: map[string]any{"type": "object", "properties": update, "required": []string{"id"}},
			Annotations: map[string]any{"readOnlyHint": false, "openWorldHint": false}},
	)
}

// automationTool calls an automation tool in the service.
func automationTool(s *mcpServer, name string, raw json.RawMessage) (string, error) {
	var args mcpAutomationArgs
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &args) != nil {
		return "", errors.New("Invalid arguments")
	}
	op := map[string]string{"list_automations": "mcpAutomationList", "create_automation": "mcpAutomationCreate", "update_automation": "mcpAutomationUpdate"}[name]
	if op == "" {
		return "", fmt.Errorf("Unknown tool %s", name)
	}
	var text string
	err := s.CallService(op, mcpRequest{Workspace: s.Workspace(), Tool: name, Auto: &args}, &text)
	return text, err
}

// mcpAutomation answers an automation tool call in the service.
func (c *chatController) mcpAutomation(op string, r mcpRequest) (any, error) {
	if c.auto == nil {
		return nil, errors.New("Automations require the Miya background service")
	}
	var args mcpAutomationArgs
	if r.Auto != nil {
		args = *r.Auto
	}
	switch op {
	case "mcpAutomationList":
		return c.auto.ListText(), nil
	case "mcpAutomationCreate":
		caller, ok := c.mcpCaller(r.Tool)
		if !ok {
			return nil, errors.New(mcpCallerUnknown)
		}
		a := automation.Automation{Name: args.Name, Project: c.threadWorkspace(caller.ID), Agent: textutil.FirstNonEmpty(args.Agent, model.ThreadAgent(caller)),
			Instructions: args.Instructions, Schedule: args.Schedule, Check: args.Check, Rounds: args.Rounds, Enabled: true}
		if a.Rounds == 0 {
			a.Rounds = 3
		}
		if err := a.Normalize(); err != nil {
			return nil, err
		}
		a.ID = ""
		id, ch := c.askUser(caller, "Create automation \""+a.Name+"\"", c.auto.CardDetail(a, c.auto.Now()))
		return deferred(func() (any, error) {
			if !c.waitUser(id, ch) {
				return "The user did not confirm. No automation was created.", nil
			}
			v, err := c.onMain(func() (any, error) {
				saved, err := c.auto.Save(a)
				if err != nil {
					return nil, err
				}
				return automation.ViewOf(saved, nil, false, c.auto.Now()), nil
			})
			if err != nil {
				return nil, err
			}
			view := v.(automation.View)
			return fmt.Sprintf("Created automation %q (ID %s), %s. Next run: %s.", view.Name, view.ID, view.ScheduleText, automation.FormatTime(view.Next)), nil
		}), nil
	case "mcpAutomationUpdate":
		return c.mcpUpdateAutomation(r, args)
	}
	return nil, fmt.Errorf("unknown miya call %s", op)
}

func (c *chatController) mcpUpdateAutomation(r mcpRequest, args mcpAutomationArgs) (any, error) {
	s := c.auto
	i, ok := s.Store.Find(strings.TrimSpace(args.ID))
	if !ok {
		for j, a := range s.Store.Automations {
			if a.Name == strings.TrimSpace(args.ID) {
				i, ok = j, true
			}
		}
	}
	if !ok {
		return nil, fmt.Errorf("Automation %q not found. Use list_automations to see available automations.", args.ID)
	}
	old := s.Store.Automations[i]
	next := old
	if args.Name != "" {
		next.Name = args.Name
	}
	if args.Schedule != "" {
		next.Schedule = args.Schedule
	}
	if args.Instructions != "" {
		next.Instructions = args.Instructions
	}
	if args.Check != "" {
		next.Check = args.Check
	}
	if args.Rounds != 0 {
		next.Rounds = args.Rounds
	}
	if args.Agent != "" {
		next.Agent = args.Agent
	}
	if args.Enabled != nil {
		next.Enabled = *args.Enabled
	}
	if err := next.Normalize(); err != nil {
		return nil, err
	}
	diff := c.auto.Diff(old, next)
	if diff == "" {
		if next.Enabled == old.Enabled {
			return "No changes to apply.", nil
		}
		// Turning one on or off needs no card: the person asked for it.
		if _, err := c.automationOp("automationToggle", automation.Request{ID: old.ID, Enabled: next.Enabled}); err != nil {
			return nil, err
		}
		if next.Enabled {
			return "Enabled \"" + old.Name + "\".", nil
		}
		return "Paused \"" + old.Name + "\".", nil
	}
	caller, ok := c.mcpCaller(r.Tool)
	if !ok {
		return nil, errors.New(mcpCallerUnknown)
	}
	id, ch := c.askUser(caller, "Update automation \""+old.Name+"\"", diff)
	return deferred(func() (any, error) {
		if !c.waitUser(id, ch) {
			return "The user did not confirm. The automation was not changed.", nil
		}
		if _, err := c.onMain(func() (any, error) { return c.auto.Save(next) }); err != nil {
			return nil, err
		}
		return "Updated \"" + next.Name + "\", " + automation.DescribeCron(next.Schedule) + ".", nil
	}), nil
}

func init() {
	registerMCPTools(nil, false, mcpTool{Name: "report_result",
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
func (c *chatController) mcpReportResult(r mcpRequest) (any, error) {
	if c.auto == nil {
		return nil, errors.New("Automations require the Miya background service")
	}
	if r.Report == nil || strings.TrimSpace(r.Report.Summary) == "" {
		return nil, errors.New("Summary is required")
	}
	caller, ok := c.mcpCaller(r.Tool)
	if !ok {
		return nil, errors.New(mcpCallerUnknown)
	}
	for id, run := range c.auto.Active {
		if run.Thread != caller.ID {
			continue
		}
		rep := r.Report.Cleaned()
		rep.Auto = false
		c.auto.Store.UpdateLastRun(id, func(x *automation.Run) { x.Report = &rep })
		c.auto.Persist()
		return "Run result recorded.", nil
	}
	return nil, errors.New("This conversation is not an active automation run. No report is needed.")
}
