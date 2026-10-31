package main

import (
	"strings"
	"time"

	"miya/internal/claude"
)

// Claude Code reports its slash commands, the user's and the project's own
// and its skills among them, as it initializes. Miya asks it once per
// project and keeps the list a while.
const (
	claudeCommandsKept   = 10 * time.Minute
	claudeCommandsFailed = time.Minute
)

type claudeCommandList struct {
	at     time.Time
	keep   time.Duration
	choice []composerChoice
}

// appUICommands are the composer's commands Miya runs itself in a Claude
// Code conversation; every other slash command goes to Claude Code.
var appUICommands = map[string]bool{"new": true, "model": true, "permissions": true, "mention": true, "diff": true, "status": true}

// appCommandNames are the commands of Miya's own list.
var appCommandNames = func() map[string]bool {
	m := map[string]bool{}
	for _, c := range composerCommands {
		m[c.Name] = true
	}
	return m
}()

// claudeTakesCommand reports whether a slash command, without its slash,
// goes to Claude Code as a message.
func claudeTakesCommand(agent, command string) bool {
	name, _, _ := strings.Cut(strings.TrimSpace(command), " ")
	return agent == agentClaude && !appUICommands[name]
}

// claudeCommandsFor returns Claude Code's commands in cwd and whether they
// are known, starting to read them when they are not.
func (c *chatController) claudeCommandsFor(cwd string) ([]composerChoice, bool) {
	if l, ok := c.claudeCmdCache[cwd]; ok {
		if time.Since(l.at) < l.keep {
			return l.choice, true
		}
		c.loadClaudeCommands(cwd)
		return l.choice, true
	}
	c.loadClaudeCommands(cwd)
	return nil, false
}

func (c *chatController) loadClaudeCommands(cwd string) {
	if cwd == "" || c.claudeCmdLoading[cwd] || c.claudeError != "" || c.ctx == nil {
		if c.claudeError != "" && c.claudeCmdCache[cwd].at.IsZero() {
			c.setClaudeCommands(cwd, nil, claudeCommandsFailed)
		}
		return
	}
	if c.claudeCmdLoading == nil {
		c.claudeCmdLoading = map[string]bool{}
	}
	c.claudeCmdLoading[cwd] = true
	binary, args, env, ctx := c.claudeBinary, c.claudeArgs, c.claudeEnv, c.ctx
	go func() {
		list, err := claude.ListCommands(ctx, binary, args, env, cwd)
		choices := make([]composerChoice, 0, len(list))
		for _, cmd := range list {
			if cmd.Name == "" {
				continue
			}
			detail := strings.TrimSpace(cmd.Description)
			if r := []rune(detail); len(r) > 140 {
				detail = string(r[:140]) + "…"
			}
			if cmd.ArgumentHint != "" {
				detail = cmd.ArgumentHint + "  " + detail
			}
			choices = append(choices, composerChoice{Name: cmd.Name, Detail: detail})
		}
		c.dispatch(func() {
			delete(c.claudeCmdLoading, cwd)
			keep := claudeCommandsKept
			if err != nil {
				keep = claudeCommandsFailed
				if old, ok := c.claudeCmdCache[cwd]; ok && len(old.choice) > 0 {
					choices = old.choice
				}
			}
			c.setClaudeCommands(cwd, choices, keep)
		})
	}()
}

func (c *chatController) setClaudeCommands(cwd string, choices []composerChoice, keep time.Duration) {
	if c.claudeCmdCache == nil {
		c.claudeCmdCache = map[string]claudeCommandList{}
	}
	c.claudeCmdCache[cwd] = claudeCommandList{at: time.Now(), keep: keep, choice: choices}
	if c.commandsChanged != nil {
		c.commandsChanged()
	}
	invalidate()
}

// commandChoices lists the commands matching query for the open
// conversation's agent: Claude Code's own after Miya's in its conversations.
func (app *chatApp) commandChoices(query string) []composerChoice {
	c := app.c
	if c.currentAgent() != agentClaude {
		return commandChoices(query)
	}
	q := strings.ToLower(query)
	seen := map[string]bool{}
	var out []composerChoice
	for _, cmd := range composerCommands {
		if appUICommands[cmd.Name] && strings.Contains(cmd.Name, q) {
			out, seen[cmd.Name] = append(out, cmd), true
		}
	}
	list, _ := c.claudeCommandsFor(c.cwd)
	for _, cmd := range list {
		if !seen[cmd.Name] && strings.Contains(strings.ToLower(cmd.Name), q) {
			out, seen[cmd.Name] = append(out, cmd), true
		}
	}
	return out
}

// refreshCommandChoices updates an open command menu once Claude Code's
// commands arrive.
func (app *chatApp) refreshCommandChoices() {
	m := &app.complete
	if m.open && m.token.Kind == '/' {
		m.choices = app.commandChoices(m.token.Query)
		m.state = app.commandMenuState()
	}
}

// phoneCommands is a project's Claude Code commands, for the phone.
type phoneCommands struct {
	Project  string           `json:"project"`
	Commands []composerChoice `json:"commands"`
}

// commandMenuState is what the command menu says when it lists nothing.
func (app *chatApp) commandMenuState() string {
	c := app.c
	if c.currentAgent() == agentClaude {
		if _, known := c.claudeCommandsFor(c.cwd); !known {
			return "正在读取 Claude Code 命令…"
		}
	}
	return "No matching commands"
}
