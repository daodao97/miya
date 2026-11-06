package appmodel

import (
	fmt "fmt"
	model "miya/internal/model"
	time "time"
)

// stalledHint tells that Claude Code has sent nothing for a while. A tool
// that runs, or an approval the user has not answered, is not a stall.
func StalledHint(t Thread, waitingForUser bool, now time.Time) string {
	if !t.Busy || model.ThreadAgent(t) != AgentClaude || t.LastOutputAtMs == 0 || waitingForUser {
		return ""
	}
	if n := len(t.Messages); n > 0 && t.Messages[n-1].Role == "tool" && t.Messages[n-1].Status == "inProgress" {
		return ""
	}
	quiet := now.Sub(time.UnixMilli(t.LastOutputAtMs))
	if quiet < claudeStallAfter {
		return ""
	}
	return fmt.Sprintf("No response for %s. Check your network or proxy, or stop and try again.", FormatElapsed(quiet.Milliseconds()))
}
