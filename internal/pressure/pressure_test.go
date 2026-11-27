package pressure

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"miya/internal/agent/quota"
)

func clockAt(m *Monitor, t time.Time) { m.now = func() time.Time { return t } }

func TestNilMonitorReportsNoPressure(t *testing.T) {
	var m *Monitor
	if m.Yield(true) || m.Agent("codex") != "" {
		t.Fatal("nil monitor held work back")
	}
}

func TestForegroundYieldIsBounded(t *testing.T) {
	m := New(nil, nil)
	start := time.Date(2026, 11, 20, 12, 0, 0, 0, time.Local)
	clockAt(m, start)
	if !m.Yield(true) {
		t.Fatal("did not yield to a running conversation")
	}
	clockAt(m, start.Add(MaxForegroundYield-time.Second))
	if !m.Yield(true) {
		t.Fatal("stopped yielding early")
	}
	clockAt(m, start.Add(MaxForegroundYield))
	if m.Yield(true) {
		t.Fatal("starved background work")
	}
	if m.Yield(false) {
		t.Fatal("yielded with nothing in the foreground")
	}
	if !m.Yield(true) {
		t.Fatal("a new foreground stretch did not start over")
	}
}

func waitLoaded(t *testing.T, m *Monitor, id string) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		m.mu.Lock()
		c := m.cache[id]
		done := c != nil && !c.loading && !c.at.IsZero()
		m.mu.Unlock()
		if done {
			return
		}
	}
	t.Fatal("reading never loaded")
}

func TestAgentUsageThresholdAndReset(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.Local)
	reading := quota.Reading{Agent: "codex", Windows: []quota.Window{{Name: "5h", Used: 40}, {Name: "weekly", Used: 95, ResetsAt: now.Add(time.Hour).Unix()}}}
	var reads atomic.Int32
	m := New(func(_ context.Context, id string) quota.Reading {
		reads.Add(1)
		return reading
	}, func(agent string) string {
		if agent == "pi" {
			return ""
		}
		return "codex"
	})
	clockAt(m, now)
	// The first check never waits for the reading.
	if got := m.Agent("codex"); got != "" {
		t.Fatalf("first check blocked or guessed: %q", got)
	}
	waitLoaded(t, m, "codex")
	if got := m.Agent("codex"); !strings.Contains(got, "95%") {
		t.Fatalf("weekly window over the threshold not reported: %q", got)
	}
	if m.Agent("pi") != "" {
		t.Fatal("agent without usage held back")
	}
	if reads.Load() != 1 {
		t.Fatalf("fresh reading read again: %d", reads.Load())
	}
	// Past its reset time the window no longer counts, even before a refresh.
	clockAt(m, now.Add(time.Hour))
	if got := m.Agent("codex"); got != "" {
		t.Fatalf("reset window still holds work: %q", got)
	}
}

func TestFailedReadingIsNoPressure(t *testing.T) {
	m := New(func(context.Context, string) quota.Reading {
		return quota.Reading{Agent: "claude", Error: "not signed in", Windows: []quota.Window{{Used: 100}}}
	}, func(string) string { return "claude" })
	m.Agent("claude-code")
	waitLoaded(t, m, "claude")
	if got := m.Agent("claude-code"); got != "" {
		t.Fatalf("failed reading held work back: %q", got)
	}
}
