package account

import (
	"context"

	"miya/internal/agent/account"
)

type Source struct{ client account.Caller }

func New(client account.Caller) account.Source { return Source{client: client} }

func (s Source) Read(ctx context.Context, cached account.Display) (account.Display, bool) {
	return ReadDisplay(ctx, s.client, cached)
}

func (s Source) ReadLimits(ctx context.Context) (account.Limits, error) {
	var result account.Limits
	err := s.client.Call(ctx, "account/rateLimits/read", map[string]any{}, &result)
	return result, err
}

func (s Source) Reset(ctx context.Context, attempt account.ResetAttempt) (account.ResetExecution, error) {
	return ExecuteReset(ctx, s.client, attempt)
}
