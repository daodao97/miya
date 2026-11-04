package main

import (
	"fmt"
	"strings"
)

// SubAgent is one sub-agent a message started or reports on. Each agent's
// adapter normalizes its own events into it (Claude Code's Task calls and the
// frames streamed under them, Codex's collab agent calls), so the chat shows
// every agent's sub-agents alike without knowing where they came from.
type SubAgent struct {
	ID string `json:"id"`
	// Name is what the sub-agent was asked to do, or who it is.
	Name   string `json:"name,omitempty"`
	Status string `json:"status"`
	// Detail is its latest step while it runs, then its last word.
	Detail string `json:"detail,omitempty"`
	// Steps counts the tools it called, when its agent reports them.
	Steps int `json:"steps,omitempty"`
}

// A sub-agent's status is one of these, whatever its agent calls them.
const (
	subAgentPending     = "pending"
	subAgentRunning     = "running"
	subAgentCompleted   = "completed"
	subAgentFailed      = "failed"
	subAgentInterrupted = "interrupted"
	subAgentUnconfirmed = "unconfirmed"
)

func subAgentActive(status string) bool {
	return status == subAgentPending || status == subAgentRunning
}

func subAgentsActive(agents []SubAgent) bool {
	for _, a := range agents {
		if subAgentActive(a.Status) {
			return true
		}
	}
	return false
}

func subAgentStatusLabel(status string) string {
	switch status {
	case subAgentPending:
		return tr("Waiting")
	case subAgentRunning:
		return tr("Running")
	case subAgentFailed:
		return tr("Failed")
	case subAgentInterrupted:
		return tr("Interrupted")
	case subAgentUnconfirmed:
		return tr("Completion unconfirmed")
	}
	return tr("Completed")
}

// subAgentSummary is the line beside a message's title: the one sub-agent's
// progress, or how many of several are in each state.
func subAgentSummary(agents []SubAgent) string {
	var parts []string
	if len(agents) == 1 {
		a := agents[0]
		if a.Steps > 0 {
			parts = append(parts, fmt.Sprintf(tr("%d steps"), a.Steps))
		}
		if subAgentActive(a.Status) && a.Detail != "" {
			parts = append(parts, firstLine(a.Detail))
		}
		return strings.Join(parts, " · ")
	}
	return subAgentCounts(agents)
}

// subAgentCounts tells how many sub-agents are in each state.
func subAgentCounts(agents []SubAgent) string {
	var parts []string
	counts := map[string]int{}
	for _, a := range agents {
		counts[a.Status]++
	}
	for _, s := range []string{subAgentRunning, subAgentPending, subAgentFailed, subAgentInterrupted, subAgentUnconfirmed, subAgentCompleted} {
		if n := counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, subAgentStatusLabel(s)))
		}
	}
	return strings.Join(parts, " · ")
}

// keepSubAgents carries the sub-agents a message already tracks over a
// replacement of it, which knows only what started them.
func keepSubAgents(t *Thread, m *Message) {
	for _, old := range t.Messages {
		if old.ItemID == m.ItemID && old.TurnID == m.TurnID && len(old.Agents) > 0 {
			m.Agents = old.Agents
			return
		}
	}
}

// latestSubAgents are the sub-agents several messages report, each once in
// its latest state: one agent can appear on several calls.
func latestSubAgents(messages []Message) []SubAgent {
	var out []SubAgent
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
