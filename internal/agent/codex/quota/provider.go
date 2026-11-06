package quota

import (
	"context"
	"errors"

	"miya/internal/agent/cli"
	"miya/internal/agent/quota"
)

type Provider struct{}

func (Provider) ID() string { return "codex" }

func (Provider) Read(ctx context.Context) (quota.Reading, error) {
	binary, home, err := cli.Location("codex")
	if err != nil {
		return quota.Reading{Agent: "codex"}, err
	}
	return readCodex(ctx, binary, home)
}

func (Provider) Reset(ctx context.Context, attempt quota.ResetAttempt) (quota.ResetResult, error) {
	binary, err := cli.FindBinary("codex")
	if err != nil {
		return quota.ResetResult{}, errors.New("codex command not found")
	}
	home, err := cli.ConfigDir("codex")
	if err != nil || home != attempt.Home {
		return quota.ResetResult{}, errors.New("The Codex configuration directory changed. Refresh and confirm again.")
	}
	return useCodexReset(ctx, binary, home, attempt)
}
