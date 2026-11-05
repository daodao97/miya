package main

import (
	"context"
	"encoding/json"
	"errors"
	"miya/internal/automation"
	"miya/internal/claude"
	"miya/internal/model"
	"miya/internal/textutil"
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
	automation.Automation
	ScheduleText string           `json:"scheduleText"`
	Next         time.Time        `json:"next,omitzero"`
	Upcoming     []time.Time      `json:"upcoming,omitempty"`
	Running      bool             `json:"running,omitempty"`
	Runs         []automation.Run `json:"runs,omitempty"`
	// TimeMissing marks a draft whose sentence named no time.
	TimeMissing bool `json:"timeMissing,omitempty"`
}

type automationRequest struct {
	ID         string                 `json:"id,omitempty"`
	Automation *automation.Automation `json:"automation,omitempty"`
	Enabled    bool                   `json:"enabled,omitempty"`
	// Request, Project and Agent ask for a draft.
	Request string `json:"request,omitempty"`
	Project string `json:"project,omitempty"`
	Agent   string `json:"agent,omitempty"`
}

var automationOps = map[string]bool{"automationList": true, "automationDraft": true, "automationSave": true,
	"automationToggle": true, "automationDelete": true, "automationRunNow": true}

func automationViewOf(a automation.Automation, runs []automation.Run, running bool, now time.Time) automationView {
	v := automationView{Automation: a, ScheduleText: automation.DescribeCron(a.Schedule), Running: running, Runs: runs}
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
	now := s.Now()
	switch op {
	case "automationList":
		views := []automationView{}
		for _, a := range s.Store.Automations {
			_, running := s.Active[a.ID]
			views = append(views, automationViewOf(a, append([]automation.Run(nil), s.Store.Runs[a.ID]...), running, now))
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
		return automationViewOf(a, s.Store.Runs[a.ID], false, now), nil
	case "automationToggle":
		i, ok := s.Store.Find(r.ID)
		if !ok {
			return nil, errors.New("Automation not found")
		}
		a := &s.Store.Automations[i]
		if r.Enabled && !a.Enabled {
			// Turning one on does not run the times it was off for.
			a.Due = now
		}
		a.Enabled = r.Enabled
		c.automationsChanged()
		return nil, nil
	case "automationDelete":
		i, ok := s.Store.Find(r.ID)
		if !ok {
			return nil, nil
		}
		s.Store.Automations = append(s.Store.Automations[:i], s.Store.Automations[i+1:]...)
		delete(s.Store.Runs, r.ID)
		// A run in progress goes on as an ordinary conversation.
		delete(s.Active, r.ID)
		c.automationsChanged()
		return nil, nil
	case "automationRunNow":
		return nil, c.auto.StartRun(r.ID, false)
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
	project, agent := textutil.FirstNonEmpty(r.Project, c.cwd), model.NormalizeAgentID(textutil.FirstNonEmpty(r.Agent, agentCodex))
	if agent == "" {
		return nil, errors.New("Agent must be codex or claude-code")
	}
	cfg, now := claude.CLI{Binary: c.claudeBinary, Args: c.claudeArgs, Env: c.claudeEnv}, c.auto.Now()
	return deferred(func() (any, error) {
		d, err := automation.DraftFor(ctx, cfg, agent, project, request, now)
		if err != nil {
			return nil, err
		}
		a, timeMissing, err := automation.FromDraft(d, agent, project, request)
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
func (c *chatController) saveAutomation(a automation.Automation) (automation.Automation, error) {
	return c.saveAutomationWithCreateID(a, "")
}

// createID lets a phone retry an interrupted creation without adding it twice.
func (c *chatController) saveAutomationWithCreateID(a automation.Automation, createID string) (automation.Automation, error) {
	s := c.auto
	now := s.Now()
	i, exists := s.Store.Find(a.ID)
	if !exists {
		a.ID, a.Created, a.Due = createID, now, now
	}
	if err := a.Normalize(); err != nil {
		return automation.Automation{}, err
	}
	if exists {
		old := s.Store.Automations[i]
		a.Created, a.Due = old.Created, old.Due
		if a.Schedule != old.Schedule || a.Enabled && !old.Enabled {
			a.Due = now
		}
		s.Store.Automations[i] = a
	} else {
		s.Store.Automations = append(s.Store.Automations, a)
	}
	c.automationsChanged()
	return a, nil
}

// automationsChanged saves the automations and keeps the login item to
// whether one is on.
func (c *chatController) automationsChanged() {
	c.auto.Persist()
	syncAutomationLaunchAgent(c.auto.Store.AnyEnabled())
	if c.revision != nil {
		c.setRevision(c.revision() + 1)
	}
}
