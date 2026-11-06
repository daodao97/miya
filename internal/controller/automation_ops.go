package controller

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"miya/internal/appmodel"
	"miya/internal/async"
	"miya/internal/automation"
	"miya/internal/claude"
	"miya/internal/model"
	"miya/internal/msgfmt"
	"miya/internal/notification"
	"miya/internal/textutil"
)

var automationOps = map[string]bool{"automationList": true, "automationDraft": true, "automationSave": true,
	"automationToggle": true, "automationDelete": true, "automationRunNow": true}

// automationCall runs an operation where the automations are, decoding its
// reply into out. A window's call waits for the service: make it off the
// main thread.
func (c *Controller) AutomationCall(op string, r automation.Request, out any) error {
	if c.Remote != nil {
		return c.Remote.Call(op, r, out)
	}
	res, err := c.AutomationOp(op, r)
	if err != nil {
		return err
	}
	if d, ok := res.(async.Reply); ok {
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
func (c *Controller) AutomationOp(op string, r automation.Request) (any, error) {
	s := c.Auto
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
		return c.prepareAutomationDraft(c.Ctx, r)
	case "automationSave":
		if r.Automation == nil {
			return nil, errors.New("No automation to save")
		}
		a, err := c.Auto.Save(*r.Automation)
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
		c.Auto.Changed()
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
		c.Auto.Changed()
		return nil, nil
	case "automationRunNow":
		return nil, c.Auto.StartRun(r.ID, false)
	}
	return nil, errors.New("Unknown automation operation: " + op)
}

// Capture configuration on the main thread; run the returned work elsewhere.
func (c *Controller) prepareAutomationDraft(ctx context.Context, r automation.Request) (async.Reply, error) {
	if c.Auto == nil {
		return nil, errors.New("Automations require the Miya background service")
	}
	request := strings.TrimSpace(r.Request)
	if request == "" {
		return nil, errors.New("Describe what you want to do first")
	}
	project, agent := textutil.FirstNonEmpty(r.Project, c.Cwd), model.NormalizeAgentID(textutil.FirstNonEmpty(r.Agent, appmodel.AgentCodex))
	if agent == "" {
		return nil, errors.New("Agent must be codex or claude-code")
	}
	cfg, now := claude.CLI{Binary: c.ClaudeBinary, Args: c.ClaudeArgs, Env: c.ClaudeEnv}, c.Auto.Now()
	return async.Reply(func() (any, error) {
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
type automationHost struct{ c *Controller }

func (h automationHost) Dispatch(f func()) { h.c.Dispatch(f) }

func (h automationHost) Submit(r model.SendRequest) int { return h.c.Submit(r) }

func (h automationHost) Thread(id int) model.Thread { return h.c.ThreadWithID(id) }

func (h automationHost) UpdateThread(id int, f func(*model.Thread)) { h.c.Update(id, f) }

func (h automationHost) AwaitsUser(t model.Thread) bool { return h.c.ThreadAwaitsUser(t) }

func (h automationHost) Outcome(id int) (string, string) { return h.c.HandoffState(id) }

func (h automationHost) AutoReport(t model.Thread) *automation.Report { return autoRunReport(t) }

func (h automationHost) Deliver(n notification.Notice) { h.c.deliverNotice(n, "completion") }

func (h automationHost) SyncLoginItem(enabled bool) { syncAutomationLaunchAgent(enabled) }

func (h automationHost) Changed() {
	if h.c.Revision != nil {
		h.c.SetRevision(h.c.Revision() + 1)
	}
}

// startAutomations loads the automations and, with schedule, checks them
// every automation.TickInterval from now on.
func (c *Controller) StartAutomations(path string, schedule bool) {
	c.Auto = automation.NewRunner(automationHost{c}, path)
	done := make(<-chan struct{})
	if c.Ctx != nil {
		done = c.Ctx.Done()
	}
	c.Auto.Start(done, schedule)
}

// autoRunReport is the result of a run whose agent reported none: its last
// reply and the files it changed.
func autoRunReport(t model.Thread) *automation.Report {
	reply := msgfmt.LastReply(t.Messages)
	files := msgfmt.ChangedFiles(t.Messages)
	if reply == "" && len(files) == 0 {
		return nil
	}
	r := automation.Report{Summary: reply, Artifacts: files, Auto: true}.Cleaned()
	return &r
}
