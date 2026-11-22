//go:build !windows

package extractor

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	claude "miya/internal/agent/claude/native"
	"miya/internal/chat/model"
	"miya/internal/memory"
	"miya/internal/memory/extractor/extractortest"
)

func TestConsolidateUsesIsolatedCodexWithItsOwnInstructions(t *testing.T) {
	_, log := extractortest.FakeCodex(t)
	job := memory.ConsolidationJob{ExtractorAgent: model.AgentCodex, Entries: []memory.ConsolidationEntry{{ID: "a", Version: 1, Kind: "fact", Content: "构建脚本是 build-ios.sh"}}}
	// The fake answers in the extraction format, which consolidation refuses.
	if _, err := Consolidate(context.Background(), claude.CLI{}, job); err == nil || !strings.Contains(err.Error(), "contract") {
		t.Fatalf("extraction-shaped answer accepted: %v", err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Args  []string       `json:"args"`
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(got.Args, " ")
	if !strings.Contains(args, "memory consolidator") || strings.Contains(args, "memory extractor") || !strings.Contains(args, "features.shell_tool=false") {
		t.Fatalf("args: %s", args)
	}
	if _, ok := got.Input["entries"]; !ok || got.Input["key"] != nil {
		t.Fatalf("input: %v", got.Input)
	}
}

func TestDecodeConsolidationIsStrict(t *testing.T) {
	if ops, err := decodeConsolidation([]byte(`{"operations":[{"action":"merge","targets":[{"id":"a","version":1},{"id":"b","version":2}],"content":"x","reason":"same"}]}`)); err != nil || len(ops) != 1 || ops[0].Targets[1].Version != 2 {
		t.Fatalf("valid result: %v %v", ops, err)
	}
	for _, raw := range []string{`{"operations":[],"extra":1}`, `{"operations":[]} {}`, `{"operations":[` + strings.TrimSuffix(strings.Repeat(`{"action":"conflict","targets":[],"content":"","reason":""},`, 9), ",") + `]}`} {
		if _, err := decodeConsolidation([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
