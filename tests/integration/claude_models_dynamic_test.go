//go:build !windows

package integration

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"miya/internal/appmodel"
	"miya/internal/controller"
	"miya/internal/prefs"
)

func TestClaudeModelsComeFromClaudeCode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := &testDispatch{}
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := `#!/bin/sh
read line
echo '{"type":"control_response","response":{"subtype":"success","request_id":"commands","response":{"commands":[],"models":[{"value":"default","resolvedModel":"claude-opus-5-5","displayName":"Default (recommended)","supportsEffort":true,"supportedEffortLevels":["low","high"]},{"value":"opus","resolvedModel":"claude-opus-5-5","displayName":"Opus","supportsEffort":true,"supportedEffortLevels":["low","high"]},{"value":"sonnet","resolvedModel":"claude-sonnet-5-5","displayName":"Sonnet","supportsEffort":true,"supportedEffortLevels":["low","high"]},{"value":"fable","resolvedModel":"claude-fable-9-9","displayName":"Fable","description":"Fable 9.9 · Most capable","supportsEffort":true,"supportedEffortLevels":["low","high"]},{"value":"auto/new-model","resolvedModel":"auto/new-model","displayName":"New Model · routing group","supportsEffort":false}]}}}'
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &controller.Controller{WindowState: appmodel.WindowState{Cwd: dir, Ctx: context.Background()}, ClaudeBinary: bin, Run: d.push}
	b := c.BackendFor(appmodel.AgentClaude)
	if got := b.ModelChoices(); !slices.Contains(got, "claude-sonnet-5-5") {
		t.Fatalf("choices before Claude Code reports %q; want Miya's own list", got)
	}
	var got []string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		d.drain()
		if got = b.ModelChoices(); slices.Contains(got, "fable") {
			break
		}
	}
	if strings.Join(got, ",") != ",sonnet,fable" {
		t.Fatalf("choices %q; want Claude Code's models but the one the default runs", got)
	}
	if b.ModelLabel("") != "Claude Opus 5.5" || b.ModelLabel("sonnet") != "Claude Sonnet 5.5" || b.ModelLabel("auto/new-model") != "New Model · routing group" || b.ModelLabel("fable") != "Claude Fable 9.9" {
		t.Fatalf("labels %q %q %q %q", b.ModelLabel(""), b.ModelLabel("sonnet"), b.ModelLabel("auto/new-model"), b.ModelLabel("fable"))
	}
	for _, o := range b.ModelOptions() {
		switch o.Value {
		case "sonnet":
			if strings.Join(o.Efforts, ",") != ",low,high" {
				t.Fatalf("sonnet efforts %q", o.Efforts)
			}
		case "auto/new-model":
			if strings.Join(o.Efforts, ",") != "" {
				t.Fatalf("routing model efforts %q; it takes none", o.Efforts)
			}
		}
	}
	b.SetModel("opus")
	if got := strings.Join(b.ModelChoices(), ","); got != "opus,sonnet,fable" {
		t.Fatalf("choices %q; the chosen model takes the default's place", got)
	}
	b.SetModel("auto/new-model")
	if got := strings.Join(b.ModelChoices(), ","); got != ",sonnet,fable,auto/new-model" {
		t.Fatalf("choices %q; a chosen routing model stays listed", got)
	}
	if !strings.Contains(prefs.Load("claudeModels"), "auto/new-model") {
		t.Fatal("models not kept for the next start")
	}
}
