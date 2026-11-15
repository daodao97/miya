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

func Automation(ctx context.Context, cfg cli.Config, project, prompt, schema string) ([]byte, error) {
	bin := cfg.Binary
	if bin == "" {
		var err error
		if bin, err = claude.Binary(); err != nil {
			return nil, err
		}
	}
	args := append(append([]string(nil), cfg.Args...), "-p", prompt, "--json-schema", schema, "--output-format", "json", "--tools", "")
	cmd := processenv.CommandContext(ctx, bin, args...)
	cmd.Dir = project
	cmd.Env = processenv.Environ(bin, cfg.Env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	processenv.Prepare(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("Claude Code could not draft the automation: %v %s", err, textutil.ClipRunes(strings.TrimSpace(stderr.String()), 300))
	}
	return claude.StructuredOutput(out, "draft the automation")
}
