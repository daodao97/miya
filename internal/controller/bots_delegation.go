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
// the Bots of the caller's project, and start_task with bot starts a new
// conversation of a Bot for the task. Agents delegate; there is no dispatch
// form.

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

// botList is the Bots of a project as list_agents shows them: the name
// start_task takes, what the Bot is for, and its agent.
func (c *Controller) botList(project string) []map[string]any {
	if project == "" {
		return nil
	}
	var out []map[string]any
	for _, b := range c.Bots() {
		if !b.WorksIn(project) {
			continue
		}
		entry := map[string]any{"name": b.Name, "agent": b.Agent}
		if b.Note != "" {
			entry["role"] = b.Note
		}
		out = append(out, entry)
	}
	return out
}

// delegationBot resolves the Bot a task names. Each task starts a new
// conversation of the Bot, with the Bot's agent, settings and access, so a
// Bot takes tasks while it works on others; no conversation is the target.
// A Bot takes tasks from the conversations of its project.
func (c *Controller) delegationBot(caller model.Thread, a coordinator.Args) (bot.Bot, string, error) {
	if a.Agent != "" || a.Model != "" || a.Effort != "" || a.Access != "" && a.Access != "read" {
		return bot.Bot{}, "", errors.New("INVALID_ARGUMENT: A Bot has its own agent, model, thinking choice and access; send bot without agent, model and effort, and with access only to narrow it to read")
	}
	b, ok := c.botNamed(a.Bot)
	if !ok {
		return bot.Bot{}, "", errors.New("INVALID_ARGUMENT: Unknown Bot; use a name from list_agents")
	}
	if !b.WorksIn(c.ThreadWorkspace(caller.ID)) {
		return bot.Bot{}, "", errors.New("ACCESS_DENIED: The Bot does not work in this project")
	}
	// read narrows the Bot's access, so it is never wider than the caller's.
	if a.Access != "read" {
		if err := c.checkBotAccess(caller, b); err != nil {
			return bot.Bot{}, "", err
		}
	}
	return b, "", nil
}

// checkBotAccess keeps a task from starting a Bot's conversation with more
// access than the delegating conversation has itself, as with start_task's
// access (docs/delegation-access.md). A Bot without an access level follows
// the window's permission choice, like any new conversation.
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
	return errors.New("ACCESS_DENIED: The Bot's access is wider than this conversation's, so a task from here cannot start a conversation of the Bot. Do the work here, or ask the person to give the Bot the task")
}

// submitBotTask submits a delegated task in a new conversation of its Bot and
// records the conversation as the Bot's. A Bot deleted after the task was
// accepted leaves the task an ordinary conversation.
func (c *Controller) submitBotTask(req model.SendRequest) int {
	if _, ok := c.bots.store.Find(req.Bot); ok {
		return c.startBotThread(req.Bot, req, true)
	}
	req.Bot = ""
	return c.SubmitExpanded(req)
}

// BotOf resolves the name of a Bot an agent wrote to the Bot's ID and agent.
// Only the Bots of the caller's project are found.
func (c *Controller) BotOf(caller model.Thread, name string) (string, string, error) {
	b, ok := c.botNamed(name)
	if !ok {
		return "", "", errors.New("Unknown Bot; use a name from list_agents")
	}
	if !b.WorksIn(c.ThreadWorkspace(caller.ID)) {
		return "", "", errors.New("The Bot does not work in this project")
	}
	return b.ID, b.Agent, nil
}
