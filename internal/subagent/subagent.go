// Package subagent normalizes the sub-agents each agent reports into one
// set of states, and summarizes them for display.
package subagent

import (
	"fmt"
	"miya/internal/model"
	"miya/internal/textutil"
	"miya/locales"
	"strings"
)

// A sub-agent's status is one of these, whatever its agent calls them.
const (
	Pending     = "pending"
	Running     = "running"
	Completed   = "completed"
	Failed      = "failed"
	Interrupted = "interrupted"
	Unconfirmed = "unconfirmed"
)

func Active(status string) bool {
	return status == Pending || status == Running
}

func AnyActive(agents []model.SubAgent) bool {
	for _, a := range agents {
		if Active(a.Status) {
			return true
		}
	}
	return false
}

func StatusLabel(status string) string {
	switch status {
	case Pending:
		return tr("Waiting")
	case Running:
		return tr("Running")
	case Failed:
		return tr("Failed")
	case Interrupted:
		return tr("Interrupted")
	case Unconfirmed:
		return tr("Completion unconfirmed")
	}
	return tr("Completed")
}

// Summary is the line beside a message's title: the one sub-agent's
// progress, or how many of several are in each state.
func Summary(agents []model.SubAgent) string {
	var parts []string
	if len(agents) == 1 {
		a := agents[0]
		if a.Steps > 0 {
			parts = append(parts, fmt.Sprintf(tr("%d steps"), a.Steps))
		}
		if Active(a.Status) && a.Detail != "" {
			parts = append(parts, textutil.FirstLine(a.Detail))
		}
		return strings.Join(parts, " · ")
	}
	return Counts(agents)
}

// Counts tells how many sub-agents are in each state.
func Counts(agents []model.SubAgent) string {
	var parts []string
	counts := map[string]int{}
	for _, a := range agents {
		counts[a.Status]++
	}
	for _, s := range []string{Running, Pending, Failed, Interrupted, Unconfirmed, Completed} {
		if n := counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, StatusLabel(s)))
		}
	}
	return strings.Join(parts, " · ")
}

// Keep carries the sub-agents a message already tracks over a
// replacement of it, which knows only what started them.
func Keep(t *model.Thread, m *model.Message) {
	for _, old := range t.Messages {
		if old.ItemID == m.ItemID && old.TurnID == m.TurnID && len(old.Agents) > 0 {
			m.Agents = old.Agents
			return
		}
	}
}

// Latest are the sub-agents several messages report, each once in
// its latest state: one agent can appear on several calls.
func Latest(messages []model.Message) []model.SubAgent {
	var out []model.SubAgent
	at := map[string]int{}
	for _, m := range messages {
		for _, a := range m.Agents {
			if i, ok := at[a.ID]; ok {
				out[i] = a
				continue
			}
			at[a.ID] = len(out)
			out = append(out, a)
		}
	}
	return out
}

func tr(s string) string { return locales.T(s) }
