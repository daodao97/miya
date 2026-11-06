package presentation

import (
	"miya/internal/chat/model"
)

// failedAfterOutput reports whether anything besides errors followed the user
// message at index user, i.e. the failed turn had already streamed output.
func FailedAfterOutput(t model.Thread, user int) bool {
	for _, m := range t.Messages[user+1:] {
		if m.Role != "error" && m.Role != "user" {
			return true
		}
	}
	return false
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
