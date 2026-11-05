package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"miya/internal/claude"
)

func persistClaudeCommand(command string) {
	path := permissionPolicyPath()
	if path == "" {
		return
	}
	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	if command == "" {
		delete(settings, "claudeCommand")
	} else {
		settings["claudeCommand"] = command
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, data, 0o600)
}

// applyClaudeCommand resolves a command for the runs that start after it.
func (c *chatController) applyClaudeCommand(line string) {
	c.claudeCommand = strings.TrimSpace(line)
	cmd, err := claude.ParseCommand(c.claudeCommand)
	c.claudeBinary, c.claudeArgs, c.claudeEnv, c.claudeError = cmd.Path, cmd.Args, cmd.Env, ""
	if err != nil {
		c.claudeBinary, c.claudeError = "", err.Error()
	}
	if c.memoryEngine != nil {
		c.memoryEngine.Configure(claude.CLI{Binary: c.claudeBinary, Args: append([]string(nil), c.claudeArgs...), Env: append([]string(nil), c.claudeEnv...)})
	}
	// Sessions live where the command's Claude Code keeps them.
	claudeConfigOverride = ""
	for _, kv := range cmd.Env {
		if v, ok := strings.CutPrefix(kv, "CLAUDE_CONFIG_DIR="); ok {
			claudeConfigOverride = v
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
