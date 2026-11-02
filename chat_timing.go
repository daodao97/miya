package main

import "miya/internal/codex"

// Protocol timestamps are seconds; durationMs preserves sub-second precision.
// Missing timestamps in older rollouts remain unknown rather than using now.
func codexTurnTimes(turn codex.Turn) (start, end int64) {
	if turn.StartedAt > 0 {
		start = turn.StartedAt * 1000
	}
	if turn.Status == "inProgress" {
		return start, 0
	}
	if turn.CompletedAt > 0 {
		end = turn.CompletedAt * 1000
	}
	if turn.DurationMS != nil && *turn.DurationMS >= 0 {
		if start > 0 {
			end = start + *turn.DurationMS
		} else if end > *turn.DurationMS {
			start = end - *turn.DurationMS
		}
	}
	return start, end
}

// Claude's transport does not attach a turn ID to its response items.
func completeClaudeTiming(t *Thread) {
	for i := len(t.Messages) - 1; i >= 0; i-- {
		m := &t.Messages[i]
		if m.Role == "user" {
			break
		}
		if t.StartedAtMs > 0 {
			m.StartedAtMs = t.StartedAtMs
		}
		m.CompletedAtMs = t.CompletedAtMs
	}
}
