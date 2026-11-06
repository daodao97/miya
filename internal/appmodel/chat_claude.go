package appmodel

import (
	"fmt"
	"time"

	agentmeta "miya/internal/agent/catalog"
	"miya/internal/model"
	"miya/internal/msgfmt"
)

// stalledHint tells that Claude Code has sent nothing for a while. A tool
// that runs, or an approval the user has not answered, is not a stall.
func StalledHint(t model.Thread, waitingForUser bool, now time.Time) string {
	if !t.Busy || agentmeta.For(model.ThreadAgent(t)).StallAfter <= 0 || t.LastOutputAtMs == 0 || waitingForUser {
		return ""
	}
	if n := len(t.Messages); n > 0 && t.Messages[n-1].Role == "tool" && t.Messages[n-1].Status == "inProgress" {
		return ""
	}
	quiet := now.Sub(time.UnixMilli(t.LastOutputAtMs))
	if quiet < agentmeta.For(model.ThreadAgent(t)).StallAfter {
		return ""
	}
	return fmt.Sprintf("No response for %s. Check your network or proxy, or stop and try again.", msgfmt.FormatElapsed(quiet.Milliseconds()))
}
