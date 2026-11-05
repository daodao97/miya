//go:build !windows

package main

import (
	"strings"
	"testing"
	"time"

	"miya/internal/claude"
	"miya/internal/memory"
	"miya/internal/memory/extractor"
	"miya/internal/memory/manager"
)

func TestMemorySuccessfulCompletionCapturedBeforeQueueAndDeduplicated(t *testing.T) {
	for _, agent := range []string{agentCodex, agentClaude} {
		t.Run(agent, func(t *testing.T) {
			c, d := queueController(t)
			attachMemory(t, c)
			scope, _ := memory.ProjectScope(c.cwd)
			if err := c.memories().Store.Configure(scope, memory.Settings{AutoExtract: true}); err != nil {
				t.Fatal(err)
			}
			c.referenceSource = testReferenceSource
			c.memories().Engine = extractor.NewIdleEngine()
			c.setThreads([]Thread{{ID: 1, Agent: agent, ServerID: "session", Busy: true, TurnID: "turn-1", Messages: []Message{{ID: 10, Role: "user", Content: "本项目手机构建使用 scripts/build-ios.sh", SentAtMs: time.Now().UnixMilli()}, {ID: 11, Role: "assistant", Content: "已确认。", SentAtMs: time.Now().UnixMilli()}}}})
			c.memories().Turns = map[int]manager.Turn{1: {LocalID: "local-turn-1", FirstMessage: 10}}
			if agent == agentCodex {
				c.handle(event("turn/completed", `{"threadId":"session","turn":{"id":"turn-1","status":"completed"}}`))
				c.handle(event("turn/completed", `{"threadId":"session","turn":{"id":"turn-1","status":"completed"}}`))
			} else {
				f := claude.Frame{Type: "result", UUID: "result-1", SessionID: "session", KeepingAlive: true, Result: "已确认。"}
				c.handleClaude(1, f)
				c.handleClaude(1, f)
			}
			waitCodexResume(t, c, d, func() bool { return c.memories().Calls == 0 })
			status, err := c.memories().Store.ExtractionStatus(scope)
			if err != nil || status.Pending != 1 {
				t.Fatalf("completion not captured exactly once: %+v %v", status, err)
			}
			_, job, ok, err := c.memories().Store.ClaimExtraction()
			if err != nil || !ok || len(job.Evidence) < 2 || job.Evidence[0].Text != "本项目手机构建使用 scripts/build-ios.sh" {
				t.Fatalf("invalid captured round: %+v %v", job, err)
			}
		})
	}
}

func TestMemoryFailedStoppedSubAgentAndHistoryDoNotExtract(t *testing.T) {
	for _, variant := range []string{"failed", "stopped", "child", "history", "automation", "control"} {
		t.Run(variant, func(t *testing.T) {
			c, d := queueController(t)
			attachMemory(t, c)
			scope, _ := memory.ProjectScope(c.cwd)
			_ = c.memories().Store.Configure(scope, memory.Settings{AutoExtract: true})
			c.memories().Engine = extractor.NewIdleEngine()
			th := Thread{ID: 1, Agent: agentClaude, ServerID: "session", Busy: true, Messages: []Message{{ID: 10, Role: "user", Content: "Memory"}, {ID: 11, Role: "assistant", Content: "回复"}}}
			if variant == "stopped" {
				th.StopRequested = true
			}
			if variant == "automation" {
				th.Automation = "scheduled"
			}
			if variant == "control" {
				th.Messages[0].MemoryControl = true
			}
			c.setThreads([]Thread{th})
			if variant != "history" {
				c.memories().Turns = map[int]manager.Turn{1: {LocalID: "local", FirstMessage: 10}}
			}
			f := claude.Frame{Type: "result", UUID: "result", KeepingAlive: true, IsError: variant == "failed"}
			if variant == "child" {
				f.ParentToolUseID = "task"
			}
			c.handleClaude(1, f)
			d.drain()
			status, _ := c.memories().Store.ExtractionStatus(scope)
			if status.Pending != 0 || c.memories().Calls != 0 {
				t.Fatal("non-user successful turn entered extraction")
			}
		})
	}
}

func TestMemoryOversizedCompletedRoundReportsSkippedExtraction(t *testing.T) {
	c, d := queueController(t)
	attachMemory(t, c)
	scope, _ := memory.ProjectScope(c.cwd)
	if err := c.memories().Store.Configure(scope, memory.Settings{AutoExtract: true}); err != nil {
		t.Fatal(err)
	}
	c.memories().Engine = extractor.NewIdleEngine()
	c.setThreads([]Thread{{ID: 1, ServerID: "session", Messages: []Message{
		{ID: 10, Role: "user", Content: strings.Repeat("问", memory.MaxExtractionInput), SentAtMs: time.Now().UnixMilli()},
		{ID: 11, Role: "assistant", Content: "Completed", SentAtMs: time.Now().UnixMilli()},
	}}})
	c.memories().Turns = map[int]manager.Turn{1: {LocalID: "local", FirstMessage: 10}}
	c.memories().CompletedTurn(1, "turn")
	waitCodexResume(t, c, d, func() bool { return c.memories().Calls == 0 })
	status, err := c.memories().Store.ExtractionStatus(scope)
	if err != nil || status.Failed != 1 || !strings.Contains(status.LastError, "input exceeds the limit") {
		t.Fatalf("skipped round has no visible diagnostic: %+v %v", status, err)
	}
	if c.memoryRevision == 0 {
		t.Fatal("skipped round did not invalidate client status")
	}
}

func TestMemoryLongFinalAnswerIsShortenedInsteadOfSkippingRound(t *testing.T) {
	c, d := queueController(t)
	attachMemory(t, c)
	scope, _ := memory.ProjectScope(c.cwd)
	if err := c.memories().Store.Configure(scope, memory.Settings{AutoExtract: true}); err != nil {
		t.Fatal(err)
	}
	c.memories().Engine = extractor.NewIdleEngine()
	answer := "构建入口是 scripts/build-ios.sh。" + strings.Repeat("答", memory.MaxExtractionInput)
	c.setThreads([]Thread{{ID: 1, ServerID: "session", Messages: []Message{
		{ID: 10, Role: "user", Content: "手机构建", SentAtMs: time.Now().UnixMilli()},
		{ID: 11, Role: "tool", Status: "completed", Content: "build succeeded", SentAtMs: time.Now().UnixMilli()},
		{ID: 12, Role: "assistant", Content: answer, SentAtMs: time.Now().UnixMilli()},
	}}})
	c.memories().Turns = map[int]manager.Turn{1: {LocalID: "local", FirstMessage: 10}}
	c.memories().CompletedTurn(1, "turn")
	waitCodexResume(t, c, d, func() bool { return c.memories().Calls == 0 })
	_, job, ok, err := c.memories().Store.ClaimExtraction()
	if err != nil || !ok {
		t.Fatalf("long final answer dropped the round: %v %v", ok, err)
	}
	// The shortened answer fills the budget; optional tool evidence yields first.
	if len(job.Evidence) != 2 || job.Evidence[0].Text != "手机构建" || job.Evidence[1].Kind != "assistant_report" {
		t.Fatalf("user statement or final answer lost: %d", len(job.Evidence))
	}
	if got := job.Evidence[1].Text; !strings.HasPrefix(answer, got) || !strings.Contains(got, "build-ios.sh") {
		t.Fatal("final answer must keep its leading text as a quotable prefix")
	}
	if _, err := job.InputJSON(); err != nil {
		t.Fatal("claimed job exceeds model input budget", err)
	}
}

func TestMemorySkippedTurnClearsFailedTurnRecord(t *testing.T) {
	c, _ := queueController(t)
	attachMemory(t, c)
	c.memories().Turns = map[int]manager.Turn{1: {LocalID: "failed", FirstMessage: 10}}
	thread := Thread{ID: 1}
	c.memories().BeginTurn(thread, SendRequest{Text: "/compact", Steer: true}, 20)
	if c.memories().Turns[1].LocalID != "failed" {
		t.Fatal("steer into a running turn must keep its record")
	}
	if got := c.memories().BeginTurn(thread, SendRequest{Text: "/compact"}, 20); got != "" {
		t.Fatalf("command turn joined memory: %q", got)
	}
	if _, ok := c.memories().Turns[1]; ok {
		t.Fatal("failed turn record would be extracted with the command's output")
	}
}
