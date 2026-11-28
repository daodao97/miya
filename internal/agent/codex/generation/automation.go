package generation

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	codex "miya/internal/agent/codex/native"
	"miya/internal/processenv"
	"miya/internal/textutil"
)

// Automation drafts an automation: Structured for that task.
func Automation(ctx context.Context, project, prompt, schemaJSON string) ([]byte, error) {
	return Structured(ctx, project, prompt, schemaJSON, "", "draft the automation")
}

// Structured asks Codex once, in a read-only sandbox, for JSON matching
// schemaJSON, of model, its default when "". task says what for, in errors:
// "draft the automation".
func Structured(ctx context.Context, project, prompt, schemaJSON, model, task string) ([]byte, error) {
	bin, err := codex.Binary()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "miya-draft-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	schema, last := filepath.Join(dir, "schema.json"), filepath.Join(dir, "last.json")
	if err := os.WriteFile(schema, []byte(schemaJSON), 0o600); err != nil {
		return nil, err
	}
	args := []string{"exec", "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only", "--output-schema", schema, "-o", last}
	if model != "" {
		args = append(args, "--model", model)
	}
	cmd := processenv.CommandContext(ctx, bin, append(args, prompt)...)
	cmd.Dir = project
	cmd.Env = processenv.Environ(bin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	processenv.Prepare(cmd)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("Codex could not %s: %v %s", task, err, textutil.ClipRunes(textutil.LastLine(stderr.String()), 300))
	}
	return os.ReadFile(last)
}
