package integration

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"

	claude "miya/internal/agent/claude/native"
	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	"miya/internal/model"
	"miya/internal/msgfmt"
)

func TestHistoryRestoresWorkedDuration(t *testing.T) {
	var remote codex.Thread
	if err := json.Unmarshal([]byte(`{"turns":[{"id":"turn1","status":"completed","startedAt":1700000000,"completedAt":1700000126,"durationMs":125500,"items":[{"id":"u","type":"userMessage","content":[{"type":"text","text":"question"}]},{"id":"a","type":"agentMessage","text":"Done"}]}]}`), &remote); err != nil {
		t.Fatal(err)
	}
	messages := msgfmt.HistoryMessages(remote)
	groups := msgfmt.GroupMessages(messages)
	if len(groups) != 2 || msgfmt.TurnElapsedLabel(groups[1].Messages, false) != "Worked for 2m 5s" {
		t.Fatalf("history lost duration: %+v", messages)
	}
	app, _ := testApp(t)
	for _, mobile := range []bool{false, true} {
		app.Mobile = mobile
		tt := ui.NewTester(func(c *ui.Context) { app.GroupView(c, groups[1]) }, 390, 200)
		tt.Frame()
		if !tt.HasText("Worked for 2m 5s") {
			t.Fatalf("footer missing on mobile=%v: %v", mobile, tt.Texts())
		}
	}
	// Repeated history reads must not turn a fixed duration into time since now.
	if got := msgfmt.TurnElapsedLabel(msgfmt.HistoryMessages(remote)[1:], false); got != "Worked for 2m 5s" {
		t.Fatal(got)
	}
}

func TestCodexTurnTimingOptionalMetadata(t *testing.T) {
	for _, tc := range []struct {
		payload    string
		start, end int64
	}{
		{`{"status":"completed"}`, 0, 0},
		{`{"status":"completed","startedAt":100,"completedAt":112}`, 100000, 112000},
		{`{"status":"completed","completedAt":112,"durationMs":11500}`, 100500, 112000},
		{`{"status":"completed","startedAt":100,"durationMs":0}`, 100000, 100000},
		{`{"status":"inProgress","startedAt":100,"completedAt":112,"durationMs":11500}`, 100000, 0},
	} {
		var turn codex.Turn
		if err := json.Unmarshal([]byte(tc.payload), &turn); err != nil {
			t.Fatal(err)
		}
		if start, end := msgfmt.CodexTurnTimes(turn); start != tc.start || end != tc.end {
			t.Fatalf("%s: got %d/%d, want %d/%d", tc.payload, start, end, tc.start, tc.end)
		}
	}
}

func TestLiveCodexUsesWholeTurnDuration(t *testing.T) {
	c := testController()
	c.Handle(event("turn/started", `{"threadId":"t1","turn":{"id":"turn1","startedAt":1700000000}}`))
	c.Handle(event("item/completed", `{"threadId":"t1","turnId":"turn1","item":{"id":"a","type":"agentMessage","text":"Done"}}`))
	c.Handle(event("turn/completed", `{"threadId":"t1","turn":{"id":"turn1","status":"completed","startedAt":1700000000,"durationMs":125500}}`))
	if got := msgfmt.TurnElapsedLabel(c.Threads()[0].Messages, false); got != "Worked for 2m 5s" {
		t.Fatalf("live footer = %q", got)
	}
}

func TestClaudeCompletionFreezesDuration(t *testing.T) {
	for _, keepAlive := range []bool{false, true} {
		c := testController()
		c.Update(1, func(th *model.Thread) {
			th.Agent = appmodel.AgentClaude
			th.StartedAtMs = 1000
			th.Messages = []model.Message{{Role: "assistant", Kind: "agentMessage", Content: "Done", StartedAtMs: 2000}}
		})
		if keepAlive {
			c.HandleClaude(1, claude.Frame{Type: "result", KeepingAlive: true, Result: "Done"})
		} else {
			c.FinishClaude(1, c.Cwd, nil)
		}
		th := c.Threads()[0]
		if th.Messages[0].StartedAtMs != 1000 || th.Messages[0].CompletedAtMs == 0 || th.Messages[0].CompletedAtMs != th.CompletedAtMs {
			t.Fatalf("completion did not freeze timing (keepAlive=%v): %+v", keepAlive, th.Messages[0])
		}
	}
}

func TestCompletedDurationNeverUsesCurrentTime(t *testing.T) {
	if got := msgfmt.TurnElapsedLabel([]model.Message{{StartedAtMs: 1000}}, false); got != "" {
		t.Fatalf("unknown completion was calculated against now: %q", got)
	}
	if got := msgfmt.TurnElapsedLabel([]model.Message{{StartedAtMs: 1000, CompletedAtMs: 12500}}, false); got != "Worked for 11s" {
		t.Fatal(got)
	}
}

func TestCompletedThreadRecoversLegacyReplyTiming(t *testing.T) {
	th := model.Thread{StartedAtMs: 1000, CompletedAtMs: 12500, Messages: []model.Message{
		{Role: "user", Content: "question"},
		{Role: "assistant", Content: "Done", StartedAtMs: 2000},
	}}
	groups := msgfmt.ThreadMessageGroups(th, msgfmt.GroupMessages(th.Messages))
	if got := msgfmt.TurnElapsedLabel(groups[1].Messages, false); got != "Worked for 11s" {
		t.Fatal(got)
	}
	if th.Messages[1].CompletedAtMs != 0 || th.Messages[1].StartedAtMs != 2000 {
		t.Fatal("presentation changed source messages")
	}
}
