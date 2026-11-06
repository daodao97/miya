package codex

import (
	"miya/internal/agent"
	"miya/internal/agent/account"
	codexaccount "miya/internal/agent/codex/account"
)

func (b Adapter) Account() account.Source {
	return codexaccount.New(b.host.Snapshot().Client)
}

var _ agent.AccountProvider = (*Adapter)(nil)
