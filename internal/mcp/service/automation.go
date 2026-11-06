package service

import (
	"errors"
	"fmt"
	"strings"

	"miya/internal/async"
	"miya/internal/automation"
	mcpproto "miya/internal/mcp/protocol"
	"miya/internal/model"
	"miya/internal/textutil"
)

func (api *Service) automation(op string, r mcpproto.Request) (any, error) {
	c := api.host
	if c.Automations() == nil {
		return nil, errors.New("Automations require the Miya background service")
	}
	var args mcpproto.AutomationArgs
	if r.Auto != nil {
		args = *r.Auto
	}
	switch op {
	case "mcpAutomationList":
		return c.Automations().ListText(), nil
	case "mcpAutomationCreate":
		caller, ok := api.Caller(r.Tool)
		if !ok {
			return nil, errors.New(mcpproto.CallerUnknown)
		}
		a := automation.Automation{Name: args.Name, Project: c.ThreadWorkspace(caller.ID), Agent: textutil.FirstNonEmpty(args.Agent, model.ThreadAgent(caller)),
			Instructions: args.Instructions, Schedule: args.Schedule, Check: args.Check, Rounds: args.Rounds, Enabled: true}
		if a.Rounds == 0 {
			a.Rounds = 3
		}
		if err := a.Normalize(); err != nil {
			return nil, err
		}
		a.ID = ""
		id, ch := c.AskUser(caller, "Create automation \""+a.Name+"\"", c.Automations().CardDetail(a, c.Automations().Now()))
		return async.Reply(func() (any, error) {
			if !c.WaitUser(id, ch) {
				return "The user did not confirm. No automation was created.", nil
			}
			v, err := c.OnMain(func() (any, error) {
				saved, err := c.Automations().Save(a)
				if err != nil {
					return nil, err
				}
				return automation.ViewOf(saved, nil, false, c.Automations().Now()), nil
			})
			if err != nil {
				return nil, err
			}
			view := v.(automation.View)
			return fmt.Sprintf("Created automation %q (ID %s), %s. Next run: %s.", view.Name, view.ID, view.ScheduleText, automation.FormatTime(view.Next)), nil
		}), nil
	case "mcpAutomationUpdate":
		return api.updateAutomation(r, args)
	}
	return nil, fmt.Errorf("unknown miya call %s", op)
}

func (api *Service) updateAutomation(r mcpproto.Request, args mcpproto.AutomationArgs) (any, error) {
	c := api.host
	s := c.Automations()
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
	diff := c.Automations().Diff(old, next)
	if diff == "" {
		if next.Enabled == old.Enabled {
			return "No changes to apply.", nil
		}
		// Turning one on or off needs no card: the person asked for it.
		if _, err := c.AutomationOp("automationToggle", automation.Request{ID: old.ID, Enabled: next.Enabled}); err != nil {
			return nil, err
		}
		if next.Enabled {
			return "Enabled \"" + old.Name + "\".", nil
		}
		return "Paused \"" + old.Name + "\".", nil
	}
	caller, ok := api.Caller(r.Tool)
	if !ok {
		return nil, errors.New(mcpproto.CallerUnknown)
	}
	id, ch := c.AskUser(caller, "Update automation \""+old.Name+"\"", diff)
	return async.Reply(func() (any, error) {
		if !c.WaitUser(id, ch) {
			return "The user did not confirm. The automation was not changed.", nil
		}
		if _, err := c.OnMain(func() (any, error) { return c.Automations().Save(next) }); err != nil {
			return nil, err
		}
		return "Updated \"" + next.Name + "\", " + automation.DescribeCron(next.Schedule) + ".", nil
	}), nil
}

func (api *Service) reportResult(r mcpproto.Request) (any, error) {
	c := api.host
	if c.Automations() == nil {
		return nil, errors.New("Automations require the Miya background service")
	}
	if r.Report == nil || strings.TrimSpace(r.Report.Summary) == "" {
		return nil, errors.New("Summary is required")
	}
	caller, ok := api.Caller(r.Tool)
	if !ok {
		return nil, errors.New(mcpproto.CallerUnknown)
	}
	for id, run := range c.Automations().Active {
		if run.Thread != caller.ID {
			continue
		}
		rep := r.Report.Cleaned()
		rep.Auto = false
		c.Automations().Store.UpdateLastRun(id, func(x *automation.Run) { x.Report = &rep })
		c.Automations().Persist()
		return "Run result recorded.", nil
	}
	return nil, errors.New("This conversation is not an active automation run. No report is needed.")
}
