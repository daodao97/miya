package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"miya/internal/automation"
	"miya/internal/model"
	"miya/internal/textutil"
)

// The miya MCP tools for automations: an agent turns what the person said
// into settings and calls create_automation; Miya shows a confirmation card
// in that conversation and creates the automation only if they accept.

type mcpAutomationArgs struct {
	ID           string `json:"id,omitempty"`
	Name         string `json:"name,omitempty"`
	Schedule     string `json:"schedule,omitempty"`
	Instructions string `json:"instructions,omitempty"`
	Check        string `json:"check,omitempty"`
	Rounds       int    `json:"rounds,omitempty"`
	Agent        string `json:"agent,omitempty"`
	Enabled      *bool  `json:"enabled,omitempty"`
}

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
	mcpTools = append(mcpTools,
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
func (s *mcpServer) automationTool(name string, raw json.RawMessage) (string, error) {
	var args mcpAutomationArgs
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &args) != nil {
		return "", errors.New("Invalid arguments")
	}
	op := map[string]string{"list_automations": "mcpAutomationList", "create_automation": "mcpAutomationCreate", "update_automation": "mcpAutomationUpdate"}[name]
	if op == "" {
		return "", fmt.Errorf("Unknown tool %s", name)
	}
	var text string
	err := s.callService(op, mcpRequest{Workspace: s.workspace, Tool: name, Auto: &args}, &text)
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
		return c.automationListText(), nil
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
		id, ch := c.askUser(caller, "Create automation \""+a.Name+"\"", automationCardDetail(a, c.auto.Now()))
		return deferred(func() (any, error) {
			if !c.waitUser(id, ch) {
				return "The user did not confirm. No automation was created.", nil
			}
			v, err := c.onMain(func() (any, error) {
				saved, err := c.saveAutomation(a)
				if err != nil {
					return nil, err
				}
				return automationViewOf(saved, nil, false, c.auto.Now()), nil
			})
			if err != nil {
				return nil, err
			}
			view := v.(automationView)
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
	diff := automationDiff(old, next)
	if diff == "" {
		if next.Enabled == old.Enabled {
			return "No changes to apply.", nil
		}
		// Turning one on or off needs no card: the person asked for it.
		if _, err := c.automationOp("automationToggle", automationRequest{ID: old.ID, Enabled: next.Enabled}); err != nil {
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
		if _, err := c.onMain(func() (any, error) { return c.saveAutomation(next) }); err != nil {
			return nil, err
		}
		return "Updated \"" + next.Name + "\", " + automation.DescribeCron(next.Schedule) + ".", nil
	}), nil
}

func (c *chatController) automationListText() string {
	s := c.auto
	if len(s.Store.Automations) == 0 {
		return "No automations yet."
	}
	var b strings.Builder
	for _, a := range s.Store.Automations {
		state := "Paused"
		if a.Enabled {
			state = "Enabled; next run: " + automation.FormatTime(a.Next(s.Now()))
		}
		fmt.Fprintf(&b, "%s\"%s\"· %s · %s · %s", a.ID, a.Name, filepath.Base(a.Project), automation.DescribeCron(a.Schedule), state)
		if r, ok := s.Store.LastRun(a.ID); ok {
			fmt.Fprintf(&b, " · Last run: %s", automation.ResultLabel(r.Result))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func automationCheckText(a automation.Automation) string {
	if a.Check == "" {
		return "No check; 1 round"
	}
	return fmt.Sprintf("Check: %s; up to %d rounds", a.Check, a.Rounds)
}

// automationCardDetail is what the confirmation card shows of a new one.
func automationCardDetail(a automation.Automation, now time.Time) string {
	return strings.Join([]string{
		fmt.Sprintf("Schedule: %s (%s); next run: %s", automation.DescribeCron(a.Schedule), a.Schedule, automation.FormatTime(a.Next(now))),
		"Project: " + a.Project,
		"Agent: " + backendFor(a.Agent).Name(),
		"Completion check: " + automationCheckText(a),
		"Instructions: " + a.Instructions,
	}, "\n")
}

// automationDiff lists what a change changes, besides turning it on or off.
func automationDiff(old, next automation.Automation) string {
	var lines []string
	add := func(label, from, to string) {
		if from != to {
			lines = append(lines, label+": "+from+" → "+to)
		}
	}
	add("Name", old.Name, next.Name)
	add("Schedule", automation.DescribeCron(old.Schedule), automation.DescribeCron(next.Schedule))
	add("Agent", backendFor(old.Agent).Name(), backendFor(next.Agent).Name())
	add("Completion check", automationCheckText(old), automationCheckText(next))
	add("Instructions", old.Instructions, next.Instructions)
	return strings.Join(lines, "\n")
}
