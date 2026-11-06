package quota

import (
	"context"

	"miya/internal/agent/cli"
	"miya/internal/agent/quota"
)

type Provider struct{}

func (Provider) ID() string { return "claude" }

func (Provider) Read(ctx context.Context) (quota.Reading, error) {
	binary, home, err := cli.Location("claude")
	if err != nil {
		return quota.Reading{Agent: "claude"}, err
	}
	windows, err := readClaude(ctx, binary, home)
	return quota.Reading{Agent: "claude", Windows: windows}, err
}
