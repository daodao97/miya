package controller

import (
	"errors"
	"strings"

	agentpolicy "miya/internal/agent/policy"
	"miya/internal/bot"
	"miya/internal/chat/model"
	"miya/internal/delegation/coordinator"
)

// Bots as delegation targets (docs/personal-agent.md, P4): list_agents lists
// the Bots of the caller's project, and start_task with bot hands a task to
// a Bot's standing conversation. Agents delegate; there is no dispatch form.

// botKey is a Bot's name or ID as a caller wrote it, without stray spaces.
func botKey(s string) string { return strings.Join(strings.Fields(s), " ") }

// botNamed finds a Bot by its ID or, as agents address it, by its name.
func (c *Controller) botNamed(key string) (bot.Bot, bool) {
	key = botKey(key)
	if key == "" {
		return bot.Bot{}, false
	}
	bots := c.Bots()
	for _, b := range bots {
		if b.ID == key {
			return b, true
		}
	}
	for _, b := range bots {
		if strings.EqualFold(b.Name, key) {
			return b, true
		}
	}
	return bot.Bot{}, false
}

// botConversation says whether a Bot's standing conversation can take a task
// now: new (the first task starts it), idle, busy, or not_loaded.
func (c *Controller) botConversation(b bot.Bot) string {
	t := c.BotThread(b)
	switch {
	case t.ID == 0 && b.Session == "":
		return "new"
	case t.ID == 0:
		return "not_loaded"
	case t.ServerID == "" || t.Busy || t.Loading || t.TaskPending || len(t.Queue) > 0:
		return "busy"
	}
	return "idle"
}

// botList is the Bots of a project as list_agents shows them: the name
// start_task takes, what the Bot is for, its agent, and the state of its
// standing conversation.
func (c *Controller) botList(project string) []map[string]any {
	if project == "" {
		return nil
	}
	var out []map[string]any
	for _, b := range c.Bots() {
		if b.Project != project {
			continue
		}
		entry := map[string]any{"name": b.Name, "agent": b.Agent, "conversation": c.botConversation(b)}
		if b.Note != "" {
			entry["role"] = b.Note
		}
		out = append(out, entry)
	}
	return out
}

// delegationBot resolves the Bot a task names to the conversation the task
// goes to: the reference of the Bot's standing conversation, or none for the
// task to start it. A Bot has its own agent, settings and access, and takes
// tasks from the conversations of its project.
func (c *Controller) delegationBot(caller model.Thread, a coordinator.Args) (bot.Bot, string, error) {
	if a.Agent != "" || a.Model != "" || a.Effort != "" || a.Access != "" {
		return bot.Bot{}, "", errors.New("INVALID_ARGUMENT: A Bot has its own agent, model, thinking choice and access; send bot without agent, model, effort and access")
	}
	b, ok := c.botNamed(a.Bot)
	if !ok {
		return bot.Bot{}, "", errors.New("INVALID_ARGUMENT: Unknown Bot; use a name from list_agents")
	}
	if b.Project != c.ThreadWorkspace(caller.ID) {
		return bot.Bot{}, "", errors.New("ACCESS_DENIED: The Bot belongs to another project")
	}
	t := c.BotThread(b)
	switch {
	case t.ID != 0 && t.ServerID == "":
		return bot.Bot{}, "", errors.New("THREAD_BUSY: The Bot's conversation is still starting")
	case t.ID != 0:
		return b, c.ThreadRef(t).URL(), nil
	case b.Session != "":
		// Never start a second conversation over one that is only not loaded.
		return bot.Bot{}, "", errors.New("CONTEXT_LOADING: The Bot's conversation is not loaded")
	}
	if err := c.checkBotAccess(caller, b); err != nil {
		return bot.Bot{}, "", err
	}
	return b, "", nil
}

// checkBotAccess keeps a task from starting a Bot's conversation with more
// access than the delegating conversation has itself, as with start_task's
// access (docs/delegation-access.md). A Bot without an access level follows
// the window's permission choice, like any new conversation, and a
// conversation that already exists keeps the permission it has.
func (c *Controller) checkBotAccess(caller model.Thread, b bot.Bot) error {
	allowed := c.permissionForThread(caller)
	switch botPolicy(b) {
	case "", agentpolicy.Read:
		return nil
	case "never":
		if allowed == "never" {
			return nil
		}
	default:
		switch allowed {
		case "on-failure", "never", agentpolicy.Edit:
			return nil
		}
	}
	return errors.New("ACCESS_DENIED: The Bot's access is wider than this conversation's, so a task from here cannot start the Bot's conversation. Ask the person to send the Bot its first message, or do the work here")
}

// submitBotTask submits the delegated task that starts a Bot's standing
// conversation and records the conversation as the Bot's. A Bot that was
// deleted, or got a conversation after the task was accepted, leaves the
// task an ordinary conversation.
func (c *Controller) submitBotTask(req model.SendRequest) int {
	if i, ok := c.bots.store.Find(req.Bot); ok && c.bots.store.Bots[i].Session == "" && !c.botPending(req.Bot) {
		return c.startBotThread(req.Bot, req)
	}
	req.Bot = ""
	return c.SubmitExpanded(req)
}

// botPending reports whether a Bot's conversation waits for its session.
func (c *Controller) botPending(id string) bool {
	for _, pending := range c.bots.pending {
		if pending == id {
			return true
		}
	}
	return false
}
