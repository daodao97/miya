package main

import (
	"context"
	"encoding/json"
	"errors"
	"miya/internal/automation"
	"miya/internal/claude"
	"miya/internal/model"
	"miya/internal/notification"
	"miya/internal/textutil"
	"strings"
)

// The Automations page, the phone and the miya MCP tools change automations
// through these operations, which run in the controller that owns them:
// windows send them to the background service.

var automationOps = map[string]bool{"automationList": true, "automationDraft": true, "automationSave": true,
	"automationToggle": true, "automationDelete": true, "automationRunNow": true}

// automationCall runs an operation where the automations are, decoding its
// reply into out. A window's call waits for the service: make it off the
// main thread.
func (c *chatController) automationCall(op string, r automation.Request, out any) error {
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
func (c *chatController) automationOp(op string, r automation.Request) (any, error) {
	s := c.auto
	if s == nil {
		return nil, errors.New("Automations require the Miya background service")
	}
	now := s.Now()
	switch op {
	case "automationList":
		views := []automation.View{}
		for _, a := range s.Store.Automations {
			_, running := s.Active[a.ID]
			views = append(views, automation.ViewOf(a, append([]automation.Run(nil), s.Store.Runs[a.ID]...), running, now))
		}
		return views, nil
	case "automationDraft":
		return c.prepareAutomationDraft(c.ctx, r)
	case "automationSave":
		if r.Automation == nil {
			return nil, errors.New("No automation to save")
		}
		a, err := c.auto.Save(*r.Automation)
		if err != nil {
			return nil, err
		}
		return automation.ViewOf(a, s.Store.Runs[a.ID], false, now), nil
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
		c.auto.Changed()
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
		c.auto.Changed()
		return nil, nil
	case "automationRunNow":
		return nil, c.auto.StartRun(r.ID, false)
	}
	return nil, errors.New("Unknown automation operation: " + op)
}

// Capture configuration on the main thread; run the returned work elsewhere.
func (c *chatController) prepareAutomationDraft(ctx context.Context, r automation.Request) (deferred, error) {
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
		v := automation.ViewOf(a, nil, false, now)
		v.TimeMissing = timeMissing
		return v, nil
	}), nil
}

// automationHost is the controller as the automation runner sees it.
type automationHost struct{ c *chatController }

func (h automationHost) Dispatch(f func())                      { h.c.dispatch(f) }
func (h automationHost) Submit(r SendRequest) int               { return h.c.submit(r) }
func (h automationHost) Thread(id int) Thread                   { return h.c.threadWithID(id) }
func (h automationHost) UpdateThread(id int, f func(*Thread))   { h.c.update(id, f) }
func (h automationHost) AwaitsUser(t Thread) bool               { return h.c.threadAwaitsUser(t) }
func (h automationHost) Outcome(id int) (string, string)        { return h.c.handoffState(id) }
func (h automationHost) AutoReport(t Thread) *automation.Report { return autoRunReport(t) }
func (h automationHost) Deliver(n notification.Notice)          { h.c.deliverNotice(n, "completion") }
func (h automationHost) SyncLoginItem(enabled bool)             { syncAutomationLaunchAgent(enabled) }

func (h automationHost) Changed() {
	if h.c.revision != nil {
		h.c.setRevision(h.c.revision() + 1)
	}
}

// startAutomations loads the automations and, with schedule, checks them
// every automation.TickInterval from now on.
func (c *chatController) startAutomations(path string, schedule bool) {
	c.auto = automation.NewRunner(automationHost{c}, path)
	done := make(<-chan struct{})
	if c.ctx != nil {
		done = c.ctx.Done()
	}
	c.auto.Start(done, schedule)
}

// autoRunReport is the result of a run whose agent reported none: its last
// reply and the files it changed.
func autoRunReport(t Thread) *automation.Report {
	reply := lastReply(t.Messages)
	files := changedFiles(t.Messages)
	if reply == "" && len(files) == 0 {
		return nil
	}
	r := automation.Report{Summary: reply, Artifacts: files, Auto: true}.Cleaned()
	return &r
}
