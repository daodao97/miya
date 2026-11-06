package msgfmt

import (
	codex "miya/internal/agent/codex/native"
	"miya/internal/model"
)

// This conversion has no controller or reactive state and may run in a worker.
func HistoryMessages(remote codex.Thread) []model.Message {
	var out []model.Message
	for _, turn := range remote.Turns {
		start, end := CodexTurnTimes(turn)
		for _, item := range turn.Items {
			if m, ok := ItemMessage(item); ok {
				if m.Status == "" {
					m.Status = turn.Status
				}
				m.ItemID = item.ID
				if m.Role == "user" {
					own, _ := model.SplitReferenceContext(m.Content)
					m.Parts = model.MessageParts(own)
				}
				m.TurnID = turn.ID
				m.SentAtMs, m.StartedAtMs, m.CompletedAtMs = start, start, end
				out = append(out, m)
			}
		}
		if turn.Error != nil {
			out = append(out, model.Message{Role: "error", Kind: "error", Status: "failed", Content: turn.Error.Message, TurnID: turn.ID, ItemID: "turn-error", SentAtMs: start, StartedAtMs: start, CompletedAtMs: end})
		}
	}
	return out
}
