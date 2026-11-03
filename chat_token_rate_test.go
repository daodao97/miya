package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"miya/internal/claude"

	"github.com/egoist/mygo/ui"
)

func TestCodexTurnOutputRate(t *testing.T) {
	c := testController()
	usage := func(total, last int) {
		t.Helper()
		data, err := json.Marshal(map[string]any{
			"threadId": "t1",
			"tokenUsage": map[string]any{
				"total": map[string]int{"outputTokens": total, "inputTokens": 1000000, "cachedInputTokens": 900000},
				"last":  map[string]int{"outputTokens": last},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		c.handle(event("thread/tokenUsage/updated", string(data)))
	}
	usage(1000, 200)
	c.handle(event("turn/started", `{"threadId":"t1","turn":{"id":"turn1"}}`))
	if got := c.threads()[0].TurnOutputTokens; got != nil {
		t.Fatal("previous turn's output leaked into the new rate")
	}
	usage(1400, 400)
	usage(1500, 100)
	usage(1500, 100) // repeated cumulative report
	th := c.threads()[0]
	if th.TurnOutputTokens == nil || *th.TurnOutputTokens != 500 {
		t.Fatalf("turn output = %v, want 500", th.TurnOutputTokens)
	}
	if got := threadTokenRateLabel(th, 10000); got != "50.0 tokens/s" {
		t.Fatalf("rate = %q", got)
	}
	if got := threadTokenRateLabel(th, 20000); got != "25.0 tokens/s" {
		t.Fatalf("rate did not follow elapsed time: %q", got)
	}
	c.handle(event("turn/started", `{"threadId":"t1","turn":{"id":"turn1"}}`))
	if got := c.threads()[0].TurnOutputTokens; got == nil || *got != 500 {
		t.Fatal("duplicate start erased the current count")
	}
	c.handle(event("turn/started", `{"threadId":"t1","turn":{"id":"turn2"}}`))
	usage(1550, 50)
	if got := c.threads()[0].TurnOutputTokens; got == nil || *got != 50 {
		t.Fatal("new turn did not reset its output baseline")
	}
	usage(10, 10) // an incomparable reset of the session total
	if c.threads()[0].TurnOutputTokens != nil {
		t.Fatal("reset total produced a misleading rate")
	}
	if c.threads()[1].TurnOutputTokens != nil {
		t.Fatal("usage leaked into another conversation")
	}
}

func TestCodexResumedOutputRate(t *testing.T) {
	c := testController()
	c.handle(event("turn/started", `{"threadId":"t1","turn":{"id":"turn1"}}`))
	c.handle(event("thread/tokenUsage/updated", `{"threadId":"t1","tokenUsage":{"last":{"outputTokens":200},"total":{"outputTokens":20000}}}`))
	if got := c.threads()[0].TurnOutputTokens; got == nil || *got != 200 {
		t.Fatal("resumed session counted historical output")
	}
	c.handle(event("thread/tokenUsage/updated", `{"threadId":"t1","tokenUsage":{"last":{"outputTokens":300},"total":{"outputTokens":20300}}}`))
	if got := c.threads()[0].TurnOutputTokens; got == nil || *got != 500 {
		t.Fatal("resumed turn did not accumulate subsequent requests")
	}
}

func TestClaudeTurnOutputRate(t *testing.T) {
	c := testController()
	emit := func(raw string) {
		t.Helper()
		var f claude.Frame
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			t.Fatal(err)
		}
		c.handleClaude(1, f)
	}
	emit(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"m1","usage":{"output_tokens":0}}}}`)
	emit(`{"type":"stream_event","event":{"type":"message_delta","usage":{"output_tokens":200}}}`)
	emit(`{"type":"assistant","message":{"id":"m1","content":[],"usage":{"output_tokens":200}}}`)
	emit(`{"type":"assistant","message":{"id":"m1","content":[],"usage":{"output_tokens":0}}}`)
	emit(`{"type":"assistant","parent_tool_use_id":"subagent","message":{"id":"child","usage":{"output_tokens":9000}}}`)
	emit(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"m2","usage":{"output_tokens":0}}}}`)
	emit(`{"type":"assistant","message":{"id":"m2","usage":{"output_tokens":60}}}`)
	th := c.threads()[0]
	if th.TurnOutputTokens == nil || *th.TurnOutputTokens != 260 {
		t.Fatalf("Claude usage was duplicated or lost: %v", th.TurnOutputTokens)
	}
	if got := threadTokenRateLabel(th, 10000); got != "26.0 tokens/s" {
		t.Fatalf("Claude rate = %q", got)
	}
	c.update(1, func(th *Thread) { th.Busy = false })
	emit(`{"type":"command_lifecycle","state":"started"}`)
	if th := c.threads()[0]; th.TurnOutputTokens != nil || th.ClaudeMessageID != "" {
		t.Fatal("new Claude turn retained old usage")
	}
	emit(`{"type":"assistant","message":{"id":"m3","usage":{"output_tokens":10}}}`)
	if got := c.threads()[0].TurnOutputTokens; got == nil || *got != 10 {
		t.Fatal("new Claude turn counted old messages")
	}
}

func TestOutputRateUnavailable(t *testing.T) {
	for _, th := range []Thread{
		{Busy: true},
		{Busy: true, TurnOutputTokens: new(100)},
		{TurnOutputTokens: new(100)},
	} {
		if got := threadTokenRateLabel(th, 0); got != "" {
			t.Fatalf("invalid duration/usage produced a rate: %q", got)
		}
	}
	if got := threadTokenRateLabel(Thread{Busy: true}, 1000); got != "" {
		t.Fatalf("unknown usage = %q", got)
	}
	if got := threadWorkingLabel(Thread{Busy: true, StartedAtMs: time.Now().Add(-time.Second).UnixMilli()}); strings.Contains(got, " · ") || strings.Contains(got, "tokens/s") {
		t.Fatalf("unknown usage left a separator or rate in the working label: %q", got)
	}
	if got := threadTokenRateLabel(Thread{Busy: true, TurnOutputTokens: new(0)}, 1000); got != "0.0 tokens/s" {
		t.Fatalf("reported zero usage = %q", got)
	}
}

func TestWorkingOutputRateReachesDesktopAndPhone(t *testing.T) {
	for _, surface := range []string{"desktop", "phone"} {
		t.Run(surface, func(t *testing.T) {
			app, tt := testApp(t)
			th := Thread{ID: 1, ServerID: "t1", Title: "Rate", Busy: true, HistoryLoaded: true,
				StartedAtMs: time.Now().Add(-10 * time.Second).UnixMilli(), TurnOutputTokens: new(400)}
			app.c.setThreads([]Thread{th})
			app.c.setActive(1)
			if surface == "phone" {
				m := testMobileApp()
				m.openThread(th)
				s := newPhoneThreadSync(nil)
				delta, _ := s.update(th)
				wire, err := json.Marshal(delta)
				if err != nil {
					t.Fatal(err)
				}
				var received phoneThreadDelta
				if err := json.Unmarshal(wire, &received); err != nil {
					t.Fatal(err)
				}
				m.receiveThread(1, received)
				tt = ui.NewTester(m.viewRoot, 390, 750)
				if n := m.view.c.current().TurnOutputTokens; n == nil || *n != 400 {
					t.Fatal("phone lost token usage during transport")
				}
			}
			tt.Frame()
			for _, text := range tt.Texts() {
				if strings.Contains(text, "Working for ") && strings.Contains(text, " tokens/s") && !strings.Contains(text, "—") {
					return
				}
			}
			t.Fatalf("working label has no output rate: %q", tt.Texts())
		})
	}
}
