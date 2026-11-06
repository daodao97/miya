//go:build !windows

package integration

import (
	"testing"
	"time"

	"miya/internal/agent/claude/native"
	"miya/internal/appmodel"
	"miya/internal/memory"
	"miya/internal/memory/extractor"
	"miya/internal/memory/manager"
	"miya/internal/model"
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
			scope, _ := memory.ProjectScope(c.Cwd)
			if err := c.Memories().Store.Configure(scope, memory.Settings{AutoExtract: true}); err != nil {
				t.Fatal(err)
			}
			c.Memories().Engine = extractor.NewIdleEngine()
			now := time.Now().UnixMilli()
			c.SetThreads([]model.Thread{{ID: 1, Agent: appmodel.AgentClaude, ServerID: "claude-session", Busy: true, Status: "Claude Code is running…", Messages: []model.Message{
				{ID: 10, Role: "user", Content: "以后手机构建都用 scripts/build-ios.sh", SentAtMs: now},
				{ID: 11, Role: "assistant", Kind: "agentMessage", Content: "好的", SentAtMs: now},
			}}})
			c.Memories().Turns = map[int]manager.Turn{1: {LocalID: "local", FirstMessage: 10}}
			c.HandleClaude(1, claude.Frame{Type: "result", UUID: "result-uuid", IsError: tc.isError, Result: "好的"})
			waitCodexResume(t, c, d, func() bool { return c.Memories().Calls == 0 })
			status, err := c.Memories().Store.ExtractionStatus(scope)
			if err != nil || status.Pending != tc.want {
				t.Fatalf("pending extractions = %d, want %d (%v)", status.Pending, tc.want, err)
			}
		})
	}
}
