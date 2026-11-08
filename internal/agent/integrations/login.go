package integrations

import (
	"context"

	claudelogin "miya/internal/agent/claude/login"
	codexlogin "miya/internal/agent/codex/login"
	codexnative "miya/internal/agent/codex/native"
	"miya/internal/agent/login"
)

func Logins(ctx context.Context) *login.Manager {
	return login.New(ctx, map[string]login.Provider{"claude": claudelogin.Provider{}, "codex": codexlogin.Provider{}})
}

func SetLoginClient(m *login.Manager, client *codexnative.Client) {
	if client != nil {
		select {
		case <-client.Done():
			client = nil
		default:
		}
	}
	m.SetProvider("codex", codexlogin.Provider{Client: client})
}
