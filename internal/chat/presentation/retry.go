package presentation

import (
	"strings"

	"miya/internal/chat/model"
)

// TurnHasOutput reports whether the latest request produced a reply or tool
// call worth preserving. Claude Code can send API failures as assistant text;
// those and empty streaming placeholders are not completed work.
func TurnHasOutput(messages []model.Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		switch m.Role {
		case "user":
			return false
		case "tool":
			return true
		case "assistant":
			text := strings.TrimSpace(m.Content)
			// Also recognize errors saved before assistant error metadata was
			// recorded, including the duplicated API error in older histories.
			if m.Status != "failed" && text != "" && !strings.HasPrefix(text, "API Error:") {
				return true
			}
		}
	}
	return false
}

// FailedAfterOutput reports whether the failed request at user produced work
// that must be kept when retrying.
func FailedAfterOutput(t model.Thread, user int) bool {
	return TurnHasOutput(t.Messages[user:])
}

// retryContinuePrompt resumes a turn that failed after it had already produced
// output, so the agent picks up where it stopped instead of starting over.
const RetryContinuePrompt = "The previous response was interrupted by an error. Continue from the completed work and tool results above. Do not repeat completed steps."

// Only the latest failed response can be retried. Old error cards must not
// start another turn after the conversation has already moved on.
func RetryableError(t model.Thread, m model.Message) bool {
	if t.ID == 0 || t.ReadOnly || m.Role != "error" || m.ItemID == "resume-error" || len(t.Messages) == 0 {
		return false
	}
	if t.Messages[len(t.Messages)-1].ID != m.ID {
		return false
	}
	for _, previous := range t.Messages[:len(t.Messages)-1] {
		if previous.Role == "user" {
			return true
		}
	}
	return false
}
