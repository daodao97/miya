package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"miya/internal/async"
	"miya/internal/automation"
	mcpproto "miya/internal/mcp/protocol"
	"miya/internal/textutil"
)

// applyWakeArgs sets the triggers and policy a tool call gives. An ingress
// trigger replacing one of old keeps its token, so that the scripts using
// it go on working.
func applyWakeArgs(a, old *automation.Automation, args mcpproto.AutomationArgs) error {
	if len(args.Triggers) > 0 && string(args.Triggers) != "null" {
		var triggers []automation.Trigger
		if err := json.Unmarshal(args.Triggers, &triggers); err != nil {
			return fmt.Errorf("Invalid triggers: %v", err)
		}
		for i := range triggers {
			t := &triggers[i]
			if t.Kind != automation.TriggerIngress || t.Token != "" || old == nil {
				continue
			}
			for _, o := range old.Triggers {
				if o.Kind == automation.TriggerIngress && (t.ID == "" || o.ID == t.ID) {
					t.ID, t.Token = o.ID, o.Token
					break
				}
			}
		}
		a.Triggers = triggers
	}
	if len(args.Policy) > 0 && string(args.Policy) != "null" {
		var p automation.WakePolicy
		if err := json.Unmarshal(args.Policy, &p); err != nil {
			return fmt.Errorf("Invalid policy: %v", err)
		}
		a.Policy = p
	}
	return nil
}

// findAutomation finds an automation by its id or its name.
func findAutomation(s *automation.Runner, ref string) (automation.Automation, bool) {
	ref = strings.TrimSpace(ref)
	if i, ok := s.Store.Find(ref); ok {
		return s.Store.Automations[i], true
	}
	for _, a := range s.Store.Automations {
		if a.Name == ref {
			return a, true
		}
	}
	return automation.Automation{}, false
}

// wakeAutomation starts a run of an automation for the calling
// conversation, once the person confirms.
func (api *Service) wakeAutomation(r mcpproto.Request, args mcpproto.AutomationArgs) (any, error) {
	c := api.host
	a, ok := findAutomation(c.Automations(), args.ID)
	if !ok {
		return nil, fmt.Errorf("Automation %q not found. Use list_automations to see available automations.", args.ID)
	}
	caller, ok := api.Caller(r.Tool)
	if !ok {
		return nil, errors.New(mcpproto.CallerUnknown)
	}
	data := strings.TrimSpace(args.Data)
	detail := "Starts a run of \"" + a.Name + "\" in " + a.Project + " now."
	if data != "" {
		detail += "\nWith: " + textutil.ClipRunes(data, 500)
	}
	id, ch := c.AskUser(caller, "Wake automation \""+a.Name+"\"", detail)
	return async.Reply(func() (any, error) {
		if !c.WaitUser(id, ch) {
			return "The user did not confirm. Nothing was started.", nil
		}
		v, err := c.OnMain(func() (any, error) {
			return c.Automations().Wake(automation.WakeEvent{Automation: a.ID, Source: automation.SourceAgent,
				Reason: "Woken from conversation \"" + textutil.FirstNonEmpty(caller.Title, "untitled") + "\"", Data: data})
		})
		if err != nil {
			return nil, err
		}
		w, _ := v.(automation.Wake)
		if w.Outcome == automation.WakeStarted {
			return "Started a run of \"" + a.Name + "\".", nil
		}
		return automation.OutcomeLabel(w.Outcome) + ": " + textutil.FirstNonEmpty(w.Detail, w.Outcome), nil
	}), nil
}
