package controller

import (
	"strings"

	claude "miya/internal/agent/claude/native"
	"miya/internal/agent/claude/native/transcript"
	claudesettings "miya/internal/agent/claude/settings"
	"miya/internal/uiruntime"
)

// applyClaudeCommand resolves a command for the runs that start after it.
func (c *Controller) applyClaudeCommand(line string) {
	c.ClaudeCommand = strings.TrimSpace(line)
	cmd, err := claude.ParseCommand(c.ClaudeCommand)
	c.ClaudeBinary, c.ClaudeArgs, c.ClaudeEnv, c.ClaudeError = cmd.Path, cmd.Args, cmd.Env, ""
	if err != nil {
		c.ClaudeBinary, c.ClaudeError = "", err.Error()
	}
	if c.Memories().Engine != nil {
		c.Memories().Engine.Configure(claude.CLI{Binary: c.ClaudeBinary, Args: append([]string(nil), c.ClaudeArgs...), Env: append([]string(nil), c.ClaudeEnv...)})
	}
	// Sessions live where the command's Claude Code keeps them.
	transcript.ConfigOverride = ""
	for _, kv := range cmd.Env {
		if v, ok := strings.CutPrefix(kv, "CLAUDE_CONFIG_DIR="); ok {
			transcript.ConfigOverride = v
		}
	}
	uiruntime.Invalidate()
}

func (c *Controller) SetClaudeCommand(line string) {
	if c.Remote != nil {
		c.forward("claudeCommand", line)
		return
	}
	c.applyClaudeCommand(line)
	claudesettings.SaveCommand(c.ClaudeCommand)
	if c.Threads != nil {
		c.loadClaudeSessions()
	}
}
