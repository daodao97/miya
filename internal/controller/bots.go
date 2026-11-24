package controller

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"miya/internal/automation"
	"miya/internal/bot"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
)

// botState is the Bots where a controller keeps them. The service owns the
// store. A window has the list the service sent, and the Bot its empty
// conversation is for.
type botState struct {
	store bot.Store
	path  string
	// pending are the conversations the person started with a Bot whose
	// agent has not named the session yet: thread ID to Bot ID.
	pending map[int]string
	// tasks are the Bot's other conversations waiting for their session: the
	// ones delegated tasks started, and the ones the person left for a new one.
	tasks map[int]string
	// reviews are the automatic reviews of conversations since the person
	// last wrote in them (bots_review.go).
	reviews map[int]reviewState
	mirror  []bot.Bot
	draft   string
}

// StartBots loads the Bots kept at path. The service calls it; a window gets
// the Bots with the service's state.
func (c *Controller) StartBots(path string) {
	c.bots.store, c.bots.path = bot.Load(path), path
	c.bots.pending, c.bots.tasks = map[int]string{}, map[int]string{}
}

// Bots lists the Bots, each with its standing conversation's thread while
// the session is not known.
func (c *Controller) Bots() []bot.Bot {
	if c.Remote != nil {
		return c.bots.mirror
	}
	out := append([]bot.Bot(nil), c.bots.store.Bots...)
	for thread, id := range c.bots.pending {
		for i := range out {
			if out[i].ID == id {
				out[i].Thread = thread
			}
		}
	}
	return out
}

// BotOp changes the Bots where they are kept: botSave adds or edits one,
// botDelete removes it, botReset makes the next message start a new
// conversation, keeping the old one the Bot's. A window's call waits for the
// service.
func (c *Controller) BotOp(op string, r bot.Request) (bot.Bot, error) {
	if c.Remote != nil {
		var out bot.Bot
		err := c.Remote.Call(op, r, &out)
		return out, err
	}
	var out bot.Bot
	switch op {
	case "botSave":
		b := r.Bot
		if err := b.Normalize(); err != nil {
			return bot.Bot{}, err
		}
		if !registeredBackend(b.Agent) {
			return bot.Bot{}, errors.New(tr("Choose an agent for the Bot"))
		}
		for _, p := range b.AllProjects() {
			if !c.knownProject(p) {
				return bot.Bot{}, errors.New(tr("Add the project to Miya before giving it a Bot"))
			}
		}
		old, existed := bot.Bot{}, false
		if i, ok := c.bots.store.Find(b.ID); ok {
			old, existed = c.bots.store.Bots[i], true
		}
		saved, err := c.bots.store.Put(b)
		if err != nil {
			return bot.Bot{}, err
		}
		if existed && (old.Agent != saved.Agent || old.Project != saved.Project) {
			c.forgetBotThread(saved.ID)
		}
		out = saved
	case "botRead":
		if !c.bots.store.ReadReports(r.ID) {
			return bot.Bot{}, nil
		}
	case "botDelete", "botReset":
		ok := false
		if op == "botDelete" {
			ok = c.bots.store.Delete(r.ID)
		} else {
			ok = c.bots.store.Reset(r.ID)
		}
		if !ok {
			return bot.Bot{}, errors.New(tr("This Bot no longer exists"))
		}
		if op == "botDelete" {
			c.forgetBotThread(r.ID)
		} else {
			c.leaveBotThread(r.ID)
		}
	default:
		return bot.Bot{}, fmt.Errorf("unknown Bot operation %q", op)
	}
	err := c.saveBots()
	if c.Revision != nil {
		c.SetRevision(c.Revision() + 1)
	}
	return out, err
}

func (c *Controller) knownProject(path string) bool {
	if path == c.Cwd {
		return true
	}
	if c.Projects != nil {
		for _, p := range c.Projects() {
			if p.Path == path {
				return true
			}
		}
	}
	return false
}

func (c *Controller) saveBots() error {
	if c.bots.path == "" {
		return nil
	}
	return c.bots.store.Save(c.bots.path)
}

// forgetBotThread drops a Bot's conversations that wait for their session.
func (c *Controller) forgetBotThread(id string) {
	for thread, pending := range c.bots.pending {
		if pending == id {
			delete(c.bots.pending, thread)
		}
	}
	for thread, task := range c.bots.tasks {
		if task == id {
			delete(c.bots.tasks, thread)
		}
	}
}

// leaveBotThread keeps the conversation the person started with a Bot, still
// waiting for its session, among the Bot's other conversations.
func (c *Controller) leaveBotThread(id string) {
	for thread, pending := range c.bots.pending {
		if pending == id {
			delete(c.bots.pending, thread)
			if c.bots.tasks == nil {
				c.bots.tasks = map[int]string{}
			}
			c.bots.tasks[thread] = id
		}
	}
}

// BotThread is the conversation the person talks to a Bot in, among the
// loaded ones, or the zero Thread.
func (c *Controller) BotThread(b bot.Bot) model.Thread {
	if b.Thread != 0 {
		return c.ThreadWithID(b.Thread)
	}
	if b.Session == "" {
		return model.Thread{}
	}
	threads := c.ProjectCache[b.Project]
	if b.Project == c.Cwd && c.Threads != nil {
		threads = c.Threads()
	}
	for _, t := range threads {
		if t.ServerID == b.Session && model.ThreadAgent(t) == b.Agent {
			return t
		}
	}
	return model.Thread{}
}

// SetDraftBot makes the window's empty conversation a message to a Bot.
func (c *Controller) SetDraftBot(id string) { c.bots.draft = id }

// CurrentBot is the Bot whose standing conversation is open, or the one the
// window's empty conversation is for.
func (c *Controller) CurrentBot() (bot.Bot, bool) {
	cur := c.Current()
	if cur.ID == 0 && c.bots.draft == "" {
		return bot.Bot{}, false
	}
	for _, b := range c.Bots() {
		if cur.ID == 0 {
			// The first message starts the conversation in the Bot's project.
			if b.ID == c.bots.draft && b.Project == c.Cwd {
				return b, true
			}
		} else if c.BotThread(b).ID == cur.ID {
			return b, true
		}
	}
	return bot.Bot{}, false
}

// botRequest points a message for a Bot at its standing conversation. When
// the Bot has none it returns the ID of the Bot the message starts one for,
// with that Bot's agent, project and settings in the request.
func (c *Controller) botRequest(req model.SendRequest) (model.SendRequest, string, error) {
	for _, b := range c.Bots() {
		if b.ID != req.Bot {
			continue
		}
		req.Model, req.Effort, req.ExplicitSettings = b.Model, b.Effort, false
		if t := c.BotThread(b); t.ID != 0 {
			// The conversation keeps the permission it was started with.
			req.ThreadID, req.Policy = t.ID, ""
			return req, "", nil
		}
		if b.Session != "" {
			// Never start a second conversation over one that is only not loaded.
			return req, "", errors.New(tr("This Bot's conversation is not loaded yet. Try again in a moment, or start a new conversation from the Bot's menu"))
		}
		req.Agent, req.Project = b.Agent, b.Project
		if policy := botPolicy(b); policy != "" {
			req.Policy = policy
		}
		return req, b.ID, nil
	}
	return req, "", errors.New(tr("This Bot no longer exists"))
}

// botPolicy is the permission a Bot's access level gives the conversation it
// starts, empty to follow the permission picked in the window.
func botPolicy(b bot.Bot) string { return automation.Access(b.Access).Policy() }

// startBotThread sends the message that starts a conversation of a Bot and
// records the conversation as the Bot's: the one the person talks to it in,
// or, for a delegated task, another one. SubmitExpanded adds the charter.
func (c *Controller) startBotThread(id string, req model.SendRequest, task bool) int {
	if _, ok := c.bots.store.Find(id); !ok {
		return 0
	}
	thread := c.SubmitExpanded(req)
	if thread == 0 {
		return 0
	}
	if !task {
		c.leaveBotThread(id)
	}
	if t := c.ThreadWithID(thread); t.ServerID != "" && task {
		c.bots.store.AddOther(id, t.ServerID)
	} else if t.ServerID != "" {
		c.bots.store.SetSession(id, t.ServerID)
	} else if task {
		if c.bots.tasks == nil {
			c.bots.tasks = map[int]string{}
		}
		c.bots.tasks[thread] = id
	} else {
		if c.bots.pending == nil {
			c.bots.pending = map[int]string{}
		}
		c.bots.pending[thread] = id
	}
	if err := c.saveBots(); err != nil && c.SetStatus != nil {
		c.SetStatus(err.Error())
	}
	if c.Revision != nil {
		c.SetRevision(c.Revision() + 1)
	}
	return thread
}

// botThreadUpdated keeps a Bot's record of its conversations as one in
// project changes: the session once the agent names it or moves it, and the
// start of each finished reply in the conversation the person talks to the
// Bot in. Update announces the change.
func (c *Controller) botThreadUpdated(old, t model.Thread, project string) {
	if len(c.bots.store.Bots) == 0 {
		return
	}
	id, current, changed := "", false, false
	if waiting, ok := c.bots.pending[t.ID]; ok {
		id, current = waiting, true
		if t.ServerID != "" {
			delete(c.bots.pending, t.ID)
			changed = c.bots.store.SetSession(id, t.ServerID)
		}
	} else if waiting, ok := c.bots.tasks[t.ID]; ok {
		id = waiting
		if t.ServerID != "" {
			delete(c.bots.tasks, t.ID)
			changed = c.bots.store.AddOther(id, t.ServerID)
		}
	} else if i, ok := c.bots.store.Match(model.ThreadAgent(old), project, old.ServerID); ok {
		b := c.bots.store.Bots[i]
		id, current = b.ID, b.Session == old.ServerID
		if t.ServerID != "" {
			changed = c.bots.store.Rename(id, old.ServerID, t.ServerID)
		}
	}
	if _, ok := c.bots.store.Find(id); !ok {
		delete(c.bots.pending, t.ID)
		delete(c.bots.tasks, t.ID)
		return
	}
	if current && old.Busy && !t.Busy {
		if preview := bot.PreviewOf(msgfmt.LastReply(t.Messages)); preview != "" {
			changed = c.bots.store.SetPreview(id, preview) || changed
		}
		if i, ok := c.bots.store.Find(id); ok {
			c.reviewTurn(c.bots.store.Bots[i], t)
		}
	}
	// A task the Bot finished is posted in its chat. Runs of automations are
	// posted by the runner, which knows when one found nothing new.
	if !current && old.Busy && !t.Busy && t.ServerID != "" && t.ExecutionID != "" && t.Automation == "" {
		title := strings.Join(strings.Fields(t.Title), " ")
		if title == "" {
			title = tr("Finished a task")
		}
		changed = c.bots.store.AddReport(id, bot.Report{Session: t.ServerID, Project: project, Kind: "task", Title: title, Summary: bot.Summary(msgfmt.LastReply(t.Messages)), At: time.Now()}) || changed
	}
	if changed {
		if err := c.saveBots(); err != nil && c.SetStatus != nil {
			c.SetStatus(err.Error())
		}
	}
}

// ThreadBot is the ID and own project of the Bot a conversation belongs to,
// or empty: MCP tools use it for the Bot's memory.
func (c *Controller) ThreadBot(t model.Thread) (string, string) {
	if b, ok := c.botOf(t, ""); ok {
		return b.ID, b.Project
	}
	return "", ""
}

// botOf is the Bot a conversation belongs to: the one a message names, or
// the one whose conversations include it.
func (c *Controller) botOf(t model.Thread, id string) (bot.Bot, bool) {
	if id == "" && t.ID != 0 {
		if id = c.bots.pending[t.ID]; id == "" {
			id = c.bots.tasks[t.ID]
		}
		if id == "" {
			if i, ok := c.bots.store.Match(model.ThreadAgent(t), c.ThreadWorkspace(t.ID), t.ServerID); ok {
				return c.bots.store.Bots[i], true
			}
		}
	}
	if i, ok := c.bots.store.Find(id); ok {
		return c.bots.store.Bots[i], true
	}
	return bot.Bot{}, false
}

// BotReportThread is the conversation of a Bot's report among the loaded
// ones, or the zero Thread.
func (c *Controller) BotReportThread(r bot.Report) model.Thread {
	threads := c.ProjectCache[r.Project]
	if r.Project == c.Cwd && c.Threads != nil {
		threads = c.Threads()
	}
	for _, t := range threads {
		if t.ServerID == r.Session && r.Session != "" {
			return t
		}
	}
	return model.Thread{}
}
