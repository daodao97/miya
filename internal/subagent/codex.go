package subagent

import (
	"encoding/json"
	"sort"
	"strings"

	"miya/internal/codex"
	"miya/internal/model"
	"miya/internal/textutil"
)

// Codex reports sub-agents on its collab agent calls: receiverThreadIds are
// the agent threads a call targets, and agentsStates, when available, the
// last known state of each, keyed by thread id.

func FromCodex(item codex.Item) []model.SubAgent {
	var states map[string]struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(item.AgentsStates, &states)
	ids := append([]string(nil), item.ReceiverThreadIDs...)
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	var more []string
	for id := range states {
		if !seen[id] {
			more = append(more, id)
		}
	}
	sort.Strings(more)
	ids = append(ids, more...)
	if len(ids) == 0 {
		return nil
	}
	agents := make([]model.SubAgent, 0, len(ids))
	for _, id := range ids {
		a := model.SubAgent{ID: id, Name: codexSubAgentName(id), Status: codexCallStatus(item.Status)}
		if s, ok := states[id]; ok {
			a.Status, a.Detail = codexSubAgentStatus(s.Status), strings.TrimSpace(s.Message)
		}
		agents = append(agents, a)
	}
	// A spawned agent is best told by what it was asked.
	if item.Tool == "spawnAgent" && len(agents) == 1 {
		if p := textutil.FirstLine(item.Prompt); p != "" {
			agents[0].Name = p
		}
	}
	return agents
}

// codexSubAgentName shortens an agent thread id to tell agents apart.
func codexSubAgentName(id string) string {
	if len(id) > 8 {
		id = id[:8]
	}
	return "Agent " + id
}

func codexSubAgentStatus(s string) string {
	switch strings.ToLower(strings.ReplaceAll(s, "_", "")) {
	case "pendinginit", "pending":
		return Pending
	case "running", "inprogress":
		return Running
	case "errored", "error", "failed", "notfound":
		return Failed
	case "interrupted":
		return Interrupted
	}
	// Completed, shut down, or a state yet unknown: not running any more.
	return Completed
}

// codexCallStatus is an agent's state when its call reports none: the
// call's own.
func codexCallStatus(s string) string {
	switch strings.ToLower(s) {
	case "inprogress", "in_progress":
		return Running
	case "failed":
		return Failed
	case "interrupted":
		return Interrupted
	}
	return Completed
}

// CodexActivity titles a sub-agent activity, an event in a
// sub-agent's life rather than a state to follow.
func CodexActivity(item codex.Item) string {
	labels := map[string]string{"started": "Subagent started", "interacted": "Subagent input sent", "interrupted": "Subagent interrupted", "completed": "Subagent completed"}
	s := textutil.FirstNonEmpty(labels[item.Kind], "Sub-agent activity")
	if who := textutil.FirstNonEmpty(item.AgentPath, item.AgentThreadID); who != "" {
		s += " · " + who
	}
	return s
}

// CodexCollabContent is what a collab call shows below its agents: what was
// sent to them, unless it already names the one agent it spawned.
func CodexCollabContent(item codex.Item, agents []model.SubAgent) string {
	prompt := strings.TrimSpace(item.Prompt)
	if len(agents) == 1 && agents[0].Name == prompt {
		return ""
	}
	return prompt
}
