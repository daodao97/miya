package generation

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	claude "miya/internal/agent/claude/native"
	"miya/internal/agent/cli"
	"miya/internal/processenv"
	"miya/internal/textutil"
)

// Automation drafts an automation: Structured for that task.
func Automation(ctx context.Context, cfg cli.Config, project, prompt, schema string) ([]byte, error) {
	return Structured(ctx, cfg, project, prompt, schema, "", "draft the automation")
}

// Structured asks Claude Code once, with no tools, for JSON matching schema,
// of model, its default when "". task says what for, in errors: "draft the
// automation".
func Structured(ctx context.Context, cfg cli.Config, project, prompt, schema, model, task string) ([]byte, error) {
	bin := cfg.Binary
	if bin == "" {
		var err error
		if bin, err = claude.Binary(); err != nil {
			return nil, err
		}
	}
	args := append(append([]string(nil), cfg.Args...), "-p", prompt, "--json-schema", schema, "--output-format", "json", "--tools", "",
		// A one-shot call is no conversation: none is saved to show in the list.
		"--no-session-persistence")
	if model != "" {
		// After the launch arguments, so it wins over a model they choose.
		args = append(args, "--model", model)
	}
	cmd := processenv.CommandContext(ctx, bin, args...)
	cmd.Dir = project
	cmd.Env = processenv.Environ(bin, cfg.Env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	processenv.Prepare(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("Claude Code could not %s: %v %s", task, err, textutil.ClipRunes(strings.TrimSpace(stderr.String()), 300))
	}
	return claude.StructuredOutput(out, task)
}
