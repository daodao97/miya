package main

import (
	"encoding/json"
	"fmt"
	"maps"

	"miya/internal/codex"
)

// Keep a turn's output separate from context/input tokens and the totals of
// earlier turns. Only the reported count travels to windows and phones.
func resetTurnOutput(t *Thread) {
	t.TurnOutputTokens = nil
	t.turnOutputBaseline = nil
	t.claudeOutputTokens = nil
	t.ClaudeMessageID = ""
	if t.Usage != nil && t.Usage.Total.OutputTokens != nil {
		t.turnOutputBaseline = new(*t.Usage.Total.OutputTokens)
	}
}

func recordCodexOutput(t *Thread, usage *codex.TokenUsage) {
	if !t.Busy || usage == nil || usage.Total.OutputTokens == nil {
		return
	}
	total := *usage.Total.OutputTokens
	if t.turnOutputBaseline == nil {
		// A resumed session may first report its historical total during this
		// turn. The last request identifies which output is new.
		last := usage.Last.OutputTokens
		if last == nil || *last < 0 || *last > total {
			return
		}
		t.turnOutputBaseline = new(total - *last)
	}
	if total < *t.turnOutputBaseline {
		// A reset/compacted total cannot be compared with the old baseline.
		t.TurnOutputTokens = nil
		return
	}
	t.TurnOutputTokens = new(total - *t.turnOutputBaseline)
}

func recordClaudeOutput(t *Thread, messageID string, raw json.RawMessage) {
	if !t.Busy || messageID == "" {
		return
	}
	var usage struct {
		OutputTokens *int `json:"output_tokens"`
	}
	if json.Unmarshal(raw, &usage) != nil || usage.OutputTokens == nil || *usage.OutputTokens < 0 {
		return
	}
	// Claude reports cumulative usage for each message, sometimes more than
	// once. Reconcile by message ID rather than adding duplicate reports.
	counts := maps.Clone(t.claudeOutputTokens)
	if counts == nil {
		counts = make(map[string]int)
	}
	counts[messageID] = max(counts[messageID], *usage.OutputTokens)
	t.claudeOutputTokens = counts
	total := 0
	for _, count := range counts {
		total += count
	}
	t.TurnOutputTokens = new(total)
}

func threadTokenRateLabel(t Thread, elapsedMs int64) string {
	if !t.Busy || t.TurnOutputTokens == nil || elapsedMs <= 0 {
		return ""
	}
	return fmt.Sprintf("%.1f tokens/s", float64(*t.TurnOutputTokens)*1000/float64(elapsedMs))
}
