package model

import (
	"net/url"
	"regexp"
	"strings"
)

// Agent backend IDs. main lists an adapter for each in agentBackends;
// TestModelBackendsMatchAgentBackends keeps the two lists equal.
const (
	AgentCodex  = "codex"
	AgentClaude = "claude-code"
)

// Backends are the agent backend IDs, in agentBackends' order.
var Backends = []string{AgentCodex, AgentClaude}

// RegisteredBackend reports whether id names an agent backend.
func RegisteredBackend(id string) bool {
	for _, b := range Backends {
		if b == id {
			return true
		}
	}
	return false
}

// SourceIDPattern matches the ID of a Miya installation in a thread URL.
var SourceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ThreadRef identifies a backend session, never a window or a sidebar row.
type ThreadRef struct {
	Source    string `json:"source"`
	Backend   string `json:"backend"`
	SessionID string `json:"sessionId"`
}

func (r ThreadRef) Valid() bool {
	return SourceIDPattern.MatchString(r.Source) && RegisteredBackend(r.Backend) && r.SessionID != "" && !strings.ContainsAny(r.SessionID, "\x00\r\n")
}

func (r ThreadRef) URL() string {
	if !r.Valid() {
		return ""
	}
	return "miya://" + r.Source + "/thread/" + r.Backend + "/" + url.PathEscape(r.SessionID)
}

// MessagePart is the lossless composer representation. The explicit text
// spelling @[title](URL) survives backend history and plain-text clipboard
// round trips; a bare URL is ordinary text and does not load any context.
type MessagePart struct {
	Type          string     `json:"type"`
	Text          string     `json:"text,omitempty"`
	Ref           *ThreadRef `json:"ref,omitempty"`
	TitleSnapshot string     `json:"titleSnapshot,omitempty"`
	Agent         string     `json:"agent,omitempty"`
}

// NormalizeAgentID maps an agent name a user or agent wrote to its backend
// ID, or "" when it names no backend.
func NormalizeAgentID(id string) string {
	normalized := strings.ToLower(strings.TrimSpace(id))
	if RegisteredBackend(normalized) {
		return normalized
	}
	switch normalized {
	case AgentCodex:
		return AgentCodex
	case AgentClaude, "claude", "claude code":
		return AgentClaude
	}
	return ""
}

// ThreadAgent is a conversation's agent; conversations from before agents
// were recorded are Codex's.
func ThreadAgent(t Thread) string {
	if t.Agent == "" {
		return AgentCodex
	}
	return t.Agent
}
