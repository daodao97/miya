//go:build !windows

package main

import (
	"testing"
	"time"

	"miya/internal/claude"
	"miya/internal/memory"
	"miya/internal/memory/extractor"
	"miya/internal/memory/manager"
)

// An ordinary Claude turn is not kept alive: its thread Status still reads
// "running" when the top-level result arrives, until the process exits. The
// successful result alone must queue extraction; an error result must not.
func TestMemoryOrdinaryClaudeResultQueuesExtraction(t *testing.T) {
	for _, tc := range []struct {
		name    string
		isError bool
		want    int
	}{{"success", false, 1}, {"error", true, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			c, d := queueController(t)
			attachMemory(t, c)
			scope, _ := memory.ProjectScope(c.cwd)
			if err := c.memories().Store.Configure(scope, memory.Settings{AutoExtract: true}); err != nil {
				t.Fatal(err)
			}
			c.memories().Engine = extractor.NewIdleEngine()
			now := time.Now().UnixMilli()
			c.setThreads([]Thread{{ID: 1, Agent: agentClaude, ServerID: "claude-session", Busy: true, Status: "Claude Code is running…", Messages: []Message{
				{ID: 10, Role: "user", Content: "以后手机构建都用 scripts/build-ios.sh", SentAtMs: now},
				{ID: 11, Role: "assistant", Kind: "agentMessage", Content: "好的", SentAtMs: now},
			}}})
			c.memories().Turns = map[int]manager.Turn{1: {LocalID: "local", FirstMessage: 10}}
			c.handleClaude(1, claude.Frame{Type: "result", UUID: "result-uuid", IsError: tc.isError, Result: "好的"})
			waitCodexResume(t, c, d, func() bool { return c.memories().Calls == 0 })
			status, err := c.memories().Store.ExtractionStatus(scope)
			if err != nil || status.Pending != tc.want {
				t.Fatalf("pending extractions = %d, want %d (%v)", status.Pending, tc.want, err)
			}
		})
	}
}
