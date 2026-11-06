package main

import (
	"strings"

	"miya/internal/claude"
	"miya/internal/claude/transcript"
)

// applyClaudeCommand resolves a command for the runs that start after it.
func (c *chatController) applyClaudeCommand(line string) {
	c.claudeCommand = strings.TrimSpace(line)
	cmd, err := claude.ParseCommand(c.claudeCommand)
	c.claudeBinary, c.claudeArgs, c.claudeEnv, c.claudeError = cmd.Path, cmd.Args, cmd.Env, ""
	if err != nil {
		c.claudeBinary, c.claudeError = "", err.Error()
	}
	if c.memories().Engine != nil {
		c.memories().Engine.Configure(claude.CLI{Binary: c.claudeBinary, Args: append([]string(nil), c.claudeArgs...), Env: append([]string(nil), c.claudeEnv...)})
	}
	// Sessions live where the command's Claude Code keeps them.
	transcript.ConfigOverride = ""
	for _, kv := range cmd.Env {
		if v, ok := strings.CutPrefix(kv, "CLAUDE_CONFIG_DIR="); ok {
			transcript.ConfigOverride = v
		}
	}
	invalidate()
}

func (c *chatController) setClaudeCommand(line string) {
	if c.remote != nil {
		c.forward("claudeCommand", line)
		return
	}
	c.applyClaudeCommand(line)
	persistClaudeCommand(c.claudeCommand)
	if c.threads != nil {
		c.loadClaudeSessions()
	}
}
