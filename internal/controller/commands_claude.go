package controller

import (
	"strings"
	"time"

	claude "miya/internal/agent/claude/native"
	"miya/internal/model"
	"miya/internal/svcproto"
	"miya/internal/uiruntime"
)

// Claude Code reports its slash commands, the user's and the project's own
// and its skills among them, as it initializes. Miya asks it once per
// project and keeps the list a while.
const (
	claudeCommandsKept   = 10 * time.Minute
	claudeCommandsFailed = time.Minute
)

type ClaudeCommandList struct {
	At     time.Time
	Keep   time.Duration
	Choice []model.ComposerChoice
}

// appUICommands are the composer's commands Miya runs itself in a Claude
// Code conversation; every other slash command goes to Claude Code.

// claudeCommandsFor returns Claude Code's commands in cwd and whether they
// are known, starting to read them when they are not.
func (c *Controller) ClaudeCommandsFor(cwd string) ([]model.ComposerChoice, bool) {
	if l, ok := c.ClaudeCmdCache[cwd]; ok {
		if time.Since(l.At) < l.Keep {
			return l.Choice, true
		}
		c.loadClaudeCommands(cwd)
		return l.Choice, true
	}
	c.loadClaudeCommands(cwd)
	return nil, false
}

func (c *Controller) loadClaudeCommands(cwd string) {
	if cwd == "" || c.claudeCmdLoading[cwd] || c.ClaudeError != "" || c.Ctx == nil {
		if c.ClaudeError != "" && c.ClaudeCmdCache[cwd].At.IsZero() {
			c.setClaudeCommands(cwd, nil, claudeCommandsFailed)
		}
		return
	}
	if c.claudeCmdLoading == nil {
		c.claudeCmdLoading = map[string]bool{}
	}
	c.claudeCmdLoading[cwd] = true
	if c.RemoteHost() {
		cl := c.Remote
		go func() {
			var list svcproto.HostCommands
			err := cl.Call("commands", cwd, &list)
			c.Dispatch(func() {
				delete(c.claudeCmdLoading, cwd)
				if err == nil && list.Known {
					c.setClaudeCommands(cwd, list.Choices, claudeCommandsKept)
				} else {
					time.AfterFunc(time.Second, func() {
						c.Dispatch(func() {
							if c.Ctx.Err() == nil {
								c.loadClaudeCommands(cwd)
							}
						})
					})
				}
			})
		}()
		return
	}
	binary, args, env, ctx := c.ClaudeBinary, c.ClaudeArgs, c.ClaudeEnv, c.Ctx
	go func() {
		list, err := claude.ListCommands(ctx, binary, args, env, cwd)
		choices := make([]model.ComposerChoice, 0, len(list))
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
			choices = append(choices, model.ComposerChoice{Name: cmd.Name, Detail: detail, ArgumentHint: cmd.ArgumentHint})
		}
		c.Dispatch(func() {
			delete(c.claudeCmdLoading, cwd)
			keep := claudeCommandsKept
			if err != nil {
				keep = claudeCommandsFailed
				if old, ok := c.ClaudeCmdCache[cwd]; ok && len(old.Choice) > 0 {
					choices = old.Choice
				}
			}
			c.setClaudeCommands(cwd, choices, keep)
		})
	}()
}

func (c *Controller) setClaudeCommands(cwd string, choices []model.ComposerChoice, keep time.Duration) {
	if c.ClaudeCmdCache == nil {
		c.ClaudeCmdCache = map[string]ClaudeCommandList{}
	}
	c.ClaudeCmdCache[cwd] = ClaudeCommandList{At: time.Now(), Keep: keep, Choice: choices}
	if c.CommandsChanged != nil {
		c.CommandsChanged()
	}
	uiruntime.Invalidate()
}
