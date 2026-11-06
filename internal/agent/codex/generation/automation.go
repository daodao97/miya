package generation

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	codex "miya/internal/agent/codex/native"
	"miya/internal/textutil"
)

func Automation(ctx context.Context, project, prompt, schemaJSON string) ([]byte, error) {
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
	cmd := exec.CommandContext(ctx, bin, "exec", "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only", "--output-schema", schema, "-o", last, prompt)
	cmd.Dir = project
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("Codex could not draft the automation: %v %s", err, textutil.ClipRunes(textutil.LastLine(stderr.String()), 300))
	}
	return os.ReadFile(last)
}
