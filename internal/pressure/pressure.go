// Package pressure tells background model work when to hold back, so the
// person's own conversations come first. Two signals count: a conversation
// the person started is running, and an agent's subscription is close to
// its limit.
//
// Miya runs agents on the person's CLI subscriptions, not on an endpoint it
// can measure, so it does not adapt to response times; the subscription's
// own usage reading is the signal. Readings are cached and refreshed in the
// background: a check never waits for one.
package pressure

import (
	"context"
	"fmt"
	"sync"
	"time"

	"miya/internal/agent/quota"
)

const (
	// Threshold is the share of any usage window, in percent, past which an
	// agent's background work waits for the window to reset.
	Threshold = 90.0
	// RefreshEvery is how old a cached reading may be before it is read again.
	RefreshEvery = 5 * time.Minute
	// MaxForegroundYield bounds how long background work waits for the
	// person's conversations in one stretch: it is delayed, never starved.
	MaxForegroundYield = 30 * time.Minute
)

// Reader reads the subscription usage of a usage ID ("codex", "claude").
type Reader func(ctx context.Context, usageID string) quota.Reading

type cached struct {
	reading quota.Reading
	at      time.Time
	loading bool
}

// Monitor is safe for concurrent use. A nil Monitor reports no pressure.
type Monitor struct {
	read    Reader
	usageID func(agent string) string
	now     func() time.Time
	mu      sync.Mutex
	cache   map[string]*cached
	since   time.Time
	// foreground is the latest SetForeground.
	foreground bool
}

// New makes a monitor that reads usage through read; usageID maps an agent
// ID to its usage ID, "" for an agent without one.
func New(read Reader, usageID func(agent string) string) *Monitor {
	return &Monitor{read: read, usageID: usageID, now: time.Now, cache: map[string]*cached{}}
}

// Yield takes whether a conversation the person started is running now and
// reports whether background work should wait for it. After
// MaxForegroundYield of continuous yielding it goes ahead anyway.
func (m *Monitor) Yield(foreground bool) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if !foreground {
		m.since = time.Time{}
		return false
	}
	if m.since.IsZero() {
		m.since = now
	}
	return now.Sub(m.since) < MaxForegroundYield
}

// SetForeground records whether a conversation the person started is
// running, for Waiting.
func (m *Monitor) SetForeground(on bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.foreground = on
	m.mu.Unlock()
}

// Waiting is Yield with the latest SetForeground.
func (m *Monitor) Waiting() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	on := m.foreground
	m.mu.Unlock()
	return m.Yield(on)
}

// Agent says why the agent's background work should hold off, or "" when
// it may run. An unknown, failed or missing reading is no pressure.
func (m *Monitor) Agent(agent string) string {
	if m == nil || m.usageID == nil {
		return ""
	}
	id := m.usageID(agent)
	if id == "" {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	c := m.cache[id]
	if c == nil {
		c = &cached{}
		m.cache[id] = c
	}
	if !c.loading && now.Sub(c.at) >= RefreshEvery && m.read != nil {
		c.loading = true
		go m.refresh(id)
	}
	return reason(c.reading, now)
}

func (m *Monitor) refresh(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	r := m.read(ctx, id)
	cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	// A failed read is kept too, so a broken CLI is not asked again at once.
	m.cache[id] = &cached{reading: r, at: m.now()}
}

func reason(r quota.Reading, now time.Time) string {
	if r.Error != "" {
		return ""
	}
	for _, w := range r.Windows {
		used := quota.Percent(w.Used)
		if used < Threshold {
			continue
		}
		if w.ResetsAt > 0 && !time.Unix(w.ResetsAt, 0).After(now) {
			continue
		}
		name := w.Name
		if name == "" {
			name = "usage"
		}
		msg := fmt.Sprintf("%s has used %.0f%% of its %s quota", quota.AgentName(r.Agent), used, name)
		if w.ResetsAt > 0 {
			msg += " until " + time.Unix(w.ResetsAt, 0).Local().Format("01-02 15:04")
		}
		return msg
	}
	return ""
}
