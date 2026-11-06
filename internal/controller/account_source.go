package controller

import (
	"miya/internal/agent"
	"miya/internal/agent/account"
	agentmeta "miya/internal/agent/catalog"
)

func (c *Controller) accountSource() account.Source {
	if provider, ok := c.BackendFor(agentmeta.Codex).(agent.AccountProvider); ok {
		return provider.Account()
	}
	return nil
}
