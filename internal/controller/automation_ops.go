package controller

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"miya/internal/agent/integrations"
	"miya/internal/appmodel"
	"miya/internal/async"
	"miya/internal/automation"
	"miya/internal/bot"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/notification"
	projectregistry "miya/internal/project/registry"
	"miya/internal/textutil"
)

var automationOps = map[string]bool{"automationList": true, "automationDraft": true, "automationSave": true,
	"automationToggle": true, "automationDelete": true, "automationRunNow": true, "automationWorktree": true, "automationWake": true, "automationWebhook": true, "automationBriefing": true, "automationReview": true}

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
			views = append(views, s.View(a, now))
		}
		return views, nil
	case "automationWebhook":
		switch r.Action {
		case "":
		case "enable", "disable":
			s.Store.WebhookDisabled = r.Action == "disable"
			s.Changed()
		default:
			return nil, errors.New("Unknown webhook action")
		}
		return s.WebhookView(), nil
	case "automationBriefing":
		if r.Action == "set" {
			return s.SetBriefing(r.Data)
		}
		return s.BriefingView(), nil
	case "automationDraft":
		return c.prepareAutomationDraft(c.Ctx, r)
	case "automationSave":
		if r.Automation == nil {
			return nil, errors.New("No automation to save")
		}
		if err := c.checkAutomationBot(r.Automation); err != nil {
			return nil, err
		}
		a, err := c.Auto.Save(*r.Automation)
		if err != nil {
			return nil, err
		}
		return s.View(a, now), nil
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
		s.Delete(r.ID)
		return nil, nil
	case "automationRunNow":
		_, err := c.Auto.Wake(automation.WakeEvent{Automation: r.ID, Source: automation.SourceManual})
		return nil, err
	case "automationWake":
		return c.Auto.Ingress(r.ID, r.Trigger, "", false, r.Data, r.Dedupe)
	case "automationWorktree":
		return nil, c.Auto.WorktreeAction(r.ID, r.Started, r.Action)
	case "automationReview":
		switch r.Action {
		case "approve", "deny":
			return nil, c.Auto.DecideReview(r.Review, r.Action == "approve", r.Scope, r.Note)
		case "handled":
			return nil, c.Auto.MarkHandled(r.ID, r.Started)
		case "revoke":
			c.Auto.RevokeGrant(r.Review)
			return nil, nil
		}
		return nil, errors.New("Unknown review action")
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
	cfg, now := c.claudeCLI(), c.Auto.Now()
	return async.Reply(func() (any, error) {
		d, err := integrations.DraftAutomation(ctx, cfg, agent, project, request, now)
		if err != nil {
			return nil, err
		}
		a, timeMissing, err := automation.FromDraft(d, agent, project, request)
		if err != nil {
			return nil, err
		}
		// A command nobody ran is not offered: the draft shows what its
		// test run found, and one that cannot run is not a draft.
		trials, err := automation.TryConditions(a)
		if err != nil {
			return nil, err
		}
		v := automation.ViewOf(a, nil, false, now)
		v.TimeMissing, v.Trials = timeMissing, trials
		return v, nil
	}), nil
}

// automationHost is the controller as the automation runner sees it.
type automationHost struct{ c *Controller }

func (h automationHost) Dispatch(f func()) { h.c.Dispatch(f) }

// Submit starts or continues a run's conversation. The first message of a
// run of an automation given to a Bot starts a conversation of the Bot, with
// its model and thinking choice; SubmitExpanded adds the Bot's charter.
func (h automationHost) Submit(r model.SendRequest) int {
	if r.Bot != "" && r.ThreadID == 0 {
		if i, ok := h.c.bots.store.Find(r.Bot); ok {
			b := h.c.bots.store.Bots[i]
			r.Model, r.Effort = b.Model, b.Effort
			return h.c.startBotThread(r.Bot, r, true)
		}
		r.Bot = ""
	}
	return h.c.Submit(r)
}

func (h automationHost) Thread(id int) model.Thread { return h.c.ThreadWithID(id) }

func (h automationHost) UpdateThread(id int, f func(*model.Thread)) { h.c.Update(id, f) }

func (h automationHost) AwaitsUser(t model.Thread) bool { return h.c.ThreadAwaitsUser(t) }

func (h automationHost) Outcome(id int) (string, string) { return h.c.HandoffState(id) }

func (h automationHost) AutoReport(t model.Thread) *automation.Report { return autoRunReport(t) }

func (h automationHost) Deliver(n notification.Notice) { h.c.deliverNotice(n, "completion") }

func (h automationHost) SyncLoginItem(enabled bool) { syncAutomationLaunchAgent(enabled) }

// AddWorkspace lists a run's worktree as a project, without trusting it in
// an agent's configuration, so that its conversation shows on both ends.
func (h automationHost) AddWorkspace(path string) {
	if items, added := projectregistry.Remember(h.c.Projects(), path); added {
		h.c.SetProjects(items)
	}
}

// RemoveWorkspace takes a removed worktree and its conversations off the
// project list; its agent configuration was never changed.
func (h automationHost) RemoveWorkspace(path string) { h.c.removeProject(path, false) }

func (h automationHost) Changed() {
	h.c.syncReviewApprovals()
	if h.c.Revision != nil {
		h.c.SetRevision(h.c.Revision() + 1)
	}
}

// startAutomations loads the automations and, with schedule, checks them
// every automation.TickInterval from now on.
func (c *Controller) StartAutomations(path string, schedule bool) {
	c.Auto = automation.NewRunner(automationHost{c}, path)
	c.Auto.Hold = c.Pressure.Agent
	c.Auto.Bot = c.automationBot
	c.Auto.Report = c.botAutomationReport
	// Reviews still waiting from before wait among the approvals again.
	c.syncReviewApprovals()
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

// automationBot is the agent and access of a Bot, for the runs of the
// automations given to it.
func (c *Controller) automationBot(id string) (string, automation.Access, bool) {
	if i, ok := c.bots.store.Find(id); ok {
		b := c.bots.store.Bots[i]
		return b.Agent, automation.Access(b.Access), true
	}
	return "", "", false
}

// checkAutomationBot keeps an automation given to a Bot in the Bot's project
// and with the Bot's agent.
func (c *Controller) checkAutomationBot(a *automation.Automation) error {
	if a.Bot == "" {
		return nil
	}
	i, ok := c.bots.store.Find(a.Bot)
	if !ok {
		return errors.New(tr("This Bot no longer exists"))
	}
	b := c.bots.store.Bots[i]
	if !b.WorksIn(a.Project) {
		return errors.New("An automation given to a Bot runs in one of the Bot's projects")
	}
	a.Agent = b.Agent
	return nil
}

// botAutomationReport posts a run of an automation given to a Bot in the
// Bot's chat.
func (c *Controller) botAutomationReport(a automation.Automation, run automation.Run, title, body string, needsUser bool) {
	session := run.ServerID
	if session == "" {
		session = c.ThreadWithID(run.Thread).ServerID
	}
	if session == "" {
		session = "run:" + a.ID + ":" + run.Started.Format(time.RFC3339)
	}
	if !c.bots.store.AddReport(a.Bot, bot.Report{Session: session, Project: a.Project, Kind: "automation", Title: title, Summary: bot.Summary(body), NeedsUser: needsUser, At: time.Now()}) {
		return
	}
	if err := c.saveBots(); err != nil && c.SetStatus != nil {
		c.SetStatus(err.Error())
	}
	if c.Revision != nil {
		c.SetRevision(c.Revision() + 1)
	}
}
