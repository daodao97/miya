package model

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

// ThreadTask is the common view of native goals and session schedules. The
// agent owns execution; Miya only projects its reported state and controls.
type ThreadTask struct {
	ID, Kind, Title, Status, Detail string
	Schedule                        string `json:",omitempty"`
	NextRunAtMs                     int64  `json:",omitempty"`
	TokensUsed, TimeUsedSeconds     int64  `json:",omitempty"`
	TokenBudget                     *int64 `json:",omitempty"`
}

type DiffLineStats struct {
	Added, Deleted int
}

type MemoryNotice struct {
	Explicit bool   `json:"explicit,omitempty"`
	Scope    string `json:"scope"`
	Project  string `json:"project,omitempty"`
	ID       string `json:"id"`
	Version  uint64 `json:"version"`
}

type QueuedMessage struct {
	ID int `json:"id"`
	SendRequest
}

// SendRequest is one message to send, with what the window chose for it.
type SendRequest struct {
	ExecutionID string `json:"executionId,omitempty"`
	Origin      string `json:"origin,omitempty"`
	// Internal/MCP/automation messages do not become automatic memories.
	MemoryControl bool `json:"memoryControl,omitempty"`
	// ThreadID is the conversation, 0 to start one of Agent.
	ThreadID int
	Agent    string
	Text     string
	Parts    []MessagePart `json:"parts,omitempty"`
	// Project is a new conversation's project, "" the open one.
	Project     string
	Attachments []string
	// Model, Effort and Policy default to the app's settings and the
	// conversation's permission choice.
	Model, Effort, Policy string
	ExplicitSettings      bool
	// Steer adds the message to a running turn instead of queueing it.
	Steer bool
	// KeepSessionSettings is used by native task controls, which must work
	// even after the user has changed the next message's launch settings.
	KeepSessionSettings bool
}
