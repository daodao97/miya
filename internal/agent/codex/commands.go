package codex

import (
	"miya/internal/agent"
)

func (b Adapter) Commands(_ string, _ bool) agent.CommandCatalog {
	return agent.DefaultCommands(b.ID())
}
