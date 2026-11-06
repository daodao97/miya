package appmodel

import (
	"strings"
)

var AppUICommands = map[string]bool{"new": true, "model": true, "permissions": true, "mention": true, "diff": true, "status": true}

// claudeTakesCommand reports whether a slash command, without its slash,
// goes to Claude Code as a message.
func ClaudeTakesCommand(agent, command string) bool {
	name, _, _ := strings.Cut(strings.TrimSpace(command), " ")
	return agent == AgentClaude && !AppUICommands[name]
}
