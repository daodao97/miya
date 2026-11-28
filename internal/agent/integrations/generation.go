package integrations

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	agentmeta "miya/internal/agent/catalog"
	claudegeneration "miya/internal/agent/claude/generation"
	claude "miya/internal/agent/claude/native"
	"miya/internal/agent/cli"
	codexgeneration "miya/internal/agent/codex/generation"
	codex "miya/internal/agent/codex/native"
	piagent "miya/internal/agent/pi"
	"miya/internal/automation"
	"miya/internal/translate"
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

// TranslateAgents is how Miya runs each agent: Claude Code's launch, and
// pi's state, nil where pi is not set up.
type TranslateAgents struct {
	Claude cli.Config
	Pi     *piagent.State
}

// translatingOrder is the order agents stand in for one that is missing.
var translatingOrder = []string{agentmeta.Codex, agentmeta.Claude, agentmeta.Pi}

// Translate asks agent r.Agent for r.Text in the language r.Lang, with its
// model r.Model, or Codex for an agent Miya does not know. When that agent
// is not installed, the first one that is translates, with its default
// model. It runs a program and waits: call it off the main thread.
func Translate(ctx context.Context, agents TranslateAgents, r translate.Request) (string, error) {
	const task = "translate the reply"
	model := func(id string) string {
		// A model is the chosen agent's: another agent would not know it.
		if id == r.Agent {
			return r.Model
		}
		return ""
	}
	generators := map[string]translate.Generator{
		agentmeta.Codex: func(ctx context.Context, project, prompt, schema string) ([]byte, error) {
			return codexgeneration.Structured(ctx, project, prompt, schema, model(agentmeta.Codex), task)
		},
		agentmeta.Claude: func(ctx context.Context, project, prompt, schema string) ([]byte, error) {
			return claudegeneration.Structured(ctx, agents.Claude, project, prompt, schema, model(agentmeta.Claude), task)
		},
		agentmeta.Pi: func(ctx context.Context, project, prompt, _ string) ([]byte, error) {
			// pi has no schema to answer to: the prompt asks for the JSON.
			out, err := piagent.Print(ctx, *agents.Pi, project, model(agentmeta.Pi),
				"You translate text exactly as asked and answer with nothing else.", prompt)
			if err != nil {
				return nil, fmt.Errorf("pi could not %s: %w", task, err)
			}
			return []byte(out), nil
		},
	}
	available := func(id string) bool {
		switch id {
		case agentmeta.Claude:
			return agents.Claude.Binary != "" || BinaryAvailable(id)
		case agentmeta.Pi:
			return agents.Pi != nil && agents.Pi.Binary != ""
		}
		return BinaryAvailable(id)
	}
	id := r.Agent
	if generators[id] == nil {
		id = agentmeta.Codex
	}
	if !available(id) {
		for _, other := range translatingOrder {
			if available(other) {
				id = other
				break
			}
		}
	}
	return translate.Run(ctx, generators[id], workDir(r.Project), r.Text, r.Lang)
}

// workDir is where a translation runs: an empty directory of its own, so
// that the agent reads no project instructions (CLAUDE.md, AGENTS.md) and
// spends nothing on them. A translation reads no files.
func workDir(string) string {
	dir := filepath.Join(os.TempDir(), "miya-translate")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return os.TempDir()
	}
	return dir
}
