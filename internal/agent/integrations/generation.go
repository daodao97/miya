package integrations

import (
	"context"
	"time"

	agentmeta "miya/internal/agent/catalog"
	claudegeneration "miya/internal/agent/claude/generation"
	claude "miya/internal/agent/claude/native"
	"miya/internal/agent/cli"
	codexgeneration "miya/internal/agent/codex/generation"
	codex "miya/internal/agent/codex/native"
	"miya/internal/automation"
)

func BinaryAvailable(id string) bool {
	finders := map[string]func() (string, error){agentmeta.Codex: codex.Binary, agentmeta.Claude: claude.Binary}
	if find := finders[id]; find != nil {
		_, err := find()
		return err == nil
	}
	return false
}

func DraftAutomation(ctx context.Context, cfg cli.Config, id, project, request string, now time.Time) (automation.Draft, error) {
	generators := map[string]automation.DraftGenerator{
		agentmeta.Codex: codexgeneration.Automation,
		agentmeta.Claude: func(ctx context.Context, project, prompt, schema string) ([]byte, error) {
			return claudegeneration.Automation(ctx, cfg, project, prompt, schema)
		},
	}
	generate := generators[id]
	if generate == nil {
		generate = generators[agentmeta.Codex]
	}
	return automation.DraftFor(ctx, generate, project, request, now)
}
