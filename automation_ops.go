package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// The Automations page, the phone and the miya MCP tools change automations
// through these operations, which run in the controller that owns them:
// windows send them to the background service.

// deferred is a reply worked out off the main thread, for requests that wait
// on other programs; the service runs it before it replies.
type deferred func() (any, error)

// automationView is an automation as the page shows it.
type automationView struct {
	Automation
	ScheduleText string          `json:"scheduleText"`
	Next         time.Time       `json:"next,omitzero"`
	Upcoming     []time.Time     `json:"upcoming,omitempty"`
	Running      bool            `json:"running,omitempty"`
	Runs         []AutomationRun `json:"runs,omitempty"`
	// TimeMissing marks a draft whose sentence named no time.
	TimeMissing bool `json:"timeMissing,omitempty"`
}

type automationRequest struct {
	ID         string      `json:"id,omitempty"`
	Automation *Automation `json:"automation,omitempty"`
	Enabled    bool        `json:"enabled,omitempty"`
	// Request, Project and Agent ask for a draft.
	Request string `json:"request,omitempty"`
	Project string `json:"project,omitempty"`
	Agent   string `json:"agent,omitempty"`
}

var automationOps = map[string]bool{"automationList": true, "automationDraft": true, "automationSave": true,
	"automationToggle": true, "automationDelete": true, "automationRunNow": true}

func automationViewOf(a Automation, runs []AutomationRun, running bool, now time.Time) automationView {
	v := automationView{Automation: a, ScheduleText: describeCron(a.Schedule), Running: running, Runs: runs}
	next := now
	for i := 0; i < 3; i++ {
		if next = a.Next(next); next.IsZero() {
			break
		}
		v.Upcoming = append(v.Upcoming, next)
	}
	if len(v.Upcoming) > 0 && a.Enabled {
		v.Next = v.Upcoming[0]
	}
	return v
}

// automationCall runs an operation where the automations are, decoding its
// reply into out. A window's call waits for the service: make it off the
// main thread.
func (c *chatController) automationCall(op string, r automationRequest, out any) error {
	if c.remote != nil {
		return c.remote.call(op, r, out)
	}
	res, err := c.automationOp(op, r)
	if err != nil {
		return err
	}
	if d, ok := res.(deferred); ok {
		if res, err = d(); err != nil {
			return err
		}
	}
	if out == nil || res == nil {
		return nil
	}
	data, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// automationOp runs an operation on the main thread of the controller that
// owns the automations.
func (c *chatController) automationOp(op string, r automationRequest) (any, error) {
	s := c.auto
	if s == nil {
		return nil, errors.New("Automations require the Miya background service")
	}
	now := s.now()
	switch op {
	case "automationList":
		views := []automationView{}
		for _, a := range s.store.Automations {
			_, running := s.active[a.ID]
			views = append(views, automationViewOf(a, append([]AutomationRun(nil), s.store.Runs[a.ID]...), running, now))
		}
		return views, nil
	case "automationDraft":
		return c.prepareAutomationDraft(c.ctx, r)
	case "automationSave":
		if r.Automation == nil {
			return nil, errors.New("No automation to save")
		}
		a, err := c.saveAutomation(*r.Automation)
		if err != nil {
			return nil, err
		}
		return automationViewOf(a, s.store.Runs[a.ID], false, now), nil
	case "automationToggle":
		i, ok := s.store.find(r.ID)
		if !ok {
			return nil, errors.New("Automation not found")
		}
		a := &s.store.Automations[i]
		if r.Enabled && !a.Enabled {
			// Turning one on does not run the times it was off for.
			a.Due = now
		}
		a.Enabled = r.Enabled
		c.automationsChanged()
		return nil, nil
	case "automationDelete":
		i, ok := s.store.find(r.ID)
		if !ok {
			return nil, nil
		}
		s.store.Automations = append(s.store.Automations[:i], s.store.Automations[i+1:]...)
		delete(s.store.Runs, r.ID)
		// A run in progress goes on as an ordinary conversation.
		delete(s.active, r.ID)
		c.automationsChanged()
		return nil, nil
	case "automationRunNow":
		return nil, c.startAutomationRun(r.ID, false)
	}
	return nil, errors.New("Unknown automation operation: " + op)
}

// Capture configuration on the main thread; run the returned work elsewhere.
func (c *chatController) prepareAutomationDraft(ctx context.Context, r automationRequest) (deferred, error) {
	if c.auto == nil {
		return nil, errors.New("Automations require the Miya background service")
	}
	request := strings.TrimSpace(r.Request)
	if request == "" {
		return nil, errors.New("Describe what you want to do first")
	}
	project, agent := firstNonEmpty(r.Project, c.cwd), normalizeAgentID(firstNonEmpty(r.Agent, agentCodex))
	if agent == "" {
		return nil, errors.New("Agent must be codex or claude-code")
	}
	cfg, now := draftConfig{c.claudeBinary, c.claudeArgs, c.claudeEnv}, c.auto.now()
	return deferred(func() (any, error) {
		d, err := draftAutomation(ctx, cfg, agent, project, request, now)
		if err != nil {
			return nil, err
		}
		a, timeMissing, err := automationFromDraft(d, agent, project, request)
		if err != nil {
			return nil, err
		}
		v := automationViewOf(a, nil, false, now)
		v.TimeMissing = timeMissing
		return v, nil
	}), nil
}

// saveAutomation creates an automation or changes one, keeping what its
// runs recorded.
func (c *chatController) saveAutomation(a Automation) (Automation, error) {
	return c.saveAutomationWithCreateID(a, "")
}

// createID lets a phone retry an interrupted creation without adding it twice.
func (c *chatController) saveAutomationWithCreateID(a Automation, createID string) (Automation, error) {
	s := c.auto
	now := s.now()
	i, exists := s.store.find(a.ID)
	if !exists {
		a.ID, a.Created, a.Due = createID, now, now
	}
	if err := a.normalize(); err != nil {
		return Automation{}, err
	}
	if exists {
		old := s.store.Automations[i]
		a.Created, a.Due = old.Created, old.Due
		if a.Schedule != old.Schedule || a.Enabled && !old.Enabled {
			a.Due = now
		}
		s.store.Automations[i] = a
	} else {
		s.store.Automations = append(s.store.Automations, a)
	}
	c.automationsChanged()
	return a, nil
}

// automationsChanged saves the automations and keeps the login item to
// whether one is on.
func (c *chatController) automationsChanged() {
	c.auto.persist()
	syncAutomationLaunchAgent(c.auto.store.anyEnabled())
	if c.revision != nil {
		c.setRevision(c.revision() + 1)
	}
}
