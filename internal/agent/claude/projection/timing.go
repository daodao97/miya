package projection

import (
	"encoding/json"
	"maps"

	"miya/internal/chat/model"
)

func CompleteClaudeTiming(t *model.Thread) {
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

// Keep a turn's output separate from context/input tokens and the totals of
// earlier turns. Only the reported count travels to windows and phones.

func RecordClaudeOutput(t *model.Thread, messageID string, raw json.RawMessage) {
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
	counts := maps.Clone(t.ClaudeOutputTokens)
	if counts == nil {
		counts = make(map[string]int)
	}
	counts[messageID] = max(counts[messageID], *usage.OutputTokens)
	t.ClaudeOutputTokens = counts
	total := 0
	for _, count := range counts {
		total += count
	}
	t.TurnOutputTokens = new(total)
}
