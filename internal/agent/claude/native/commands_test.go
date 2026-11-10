//go:build !windows

package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestListCommands(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "claude")
	script := `#!/bin/sh
read line
echo '{"type":"system","subtype":"hook_started"}'
echo '{"type":"control_response","response":{"subtype":"success","request_id":"commands","response":{"commands":[{"name":"review","description":"Review","argumentHint":""}]}}}'
echo '{"type":"system","subtype":"commands_changed","commands":[{"name":"review","description":"Review","argumentHint":""},{"name":"lark-im","description":"IM","argumentHint":"[chat]"}]}'
sleep 10
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	cmds, err := ListCommands(context.Background(), fake, nil, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 2 || cmds[1].Name != "lark-im" || cmds[1].ArgumentHint != "[chat]" {
		t.Fatalf("commands = %+v; want the completed list", cmds)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %v; should stop once the list is complete", time.Since(start))
	}
}

func TestReadCatalogReadsModels(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "claude")
	script := `#!/bin/sh
read line
echo '{"type":"control_response","response":{"subtype":"success","request_id":"commands","response":{"commands":[],"models":[{"value":"default","resolvedModel":"claude-opus-5-5","displayName":"Default (recommended)","supportsEffort":true,"supportedEffortLevels":["low","high"]},{"value":"auto/x","resolvedModel":"auto/x","displayName":"X · routing group","supportsEffort":false}]}}}'
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cat, err := ReadCatalog(context.Background(), fake, nil, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Models) != 2 || cat.Models[0].ResolvedModel != "claude-opus-5-5" || len(cat.Models[0].SupportedEffortLevels) != 2 || cat.Models[1].DisplayName != "X · routing group" || cat.Models[1].SupportsEffort {
		t.Fatalf("models = %+v", cat.Models)
	}
}
