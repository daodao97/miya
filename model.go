package main

import "miya/internal/codex"

type Message struct {
	MemoryControl  bool          `json:"memoryControl,omitempty"`
	MemoryTurnID   string        `json:"memoryTurnId,omitempty"`
	Memory         *memoryNotice `json:"memory,omitempty"`
	HandoffTitle   string        `json:"handoffTitle,omitempty"`
	Parts          []MessagePart `json:"parts,omitempty"`
	Images         []string
	Kind           string
	Status         string
	Item           *codex.Item
	Plan           []codex.PlanStep
	SentAtMs       int64
	StartedAtMs    int64
	CompletedAtMs  int64
	ID             int
	Role, Content  string
	ItemID, TurnID string
	// Agents are the sub-agents the message started or reports on, as its
	// agent's adapter normalized them.
	Agents []SubAgent `json:",omitempty"`
}

type Thread struct {
	MemoryDisabled       bool   `json:"memoryDisabled,omitempty"`
	MemoryEpoch          uint64 `json:"memoryEpoch,omitempty"`
	CompletionNoticeBody string `json:"completionNoticeBody,omitempty"`
	Agent                string
	Tasks                []ThreadTask `json:",omitempty"`
	TaskPending          bool         `json:",omitempty"`
	TaskStopping         bool         `json:",omitempty"`
	goalRevision         uint64
	// Automation is the automation that started the conversation.
	Automation                  string `json:",omitempty"`
	ClaudeMessageID             string
	Usage                       *codex.TokenUsage
	PermissionPolicy            string
	HistoryLoaded               bool
	Loading                     bool
	Tokens                      int
	ID                          int
	Title, Project, Age, Draft  string
	ServerID, TurnID, Status    string
	StartedAtMs, CompletedAtMs  int64
	LastActivityAt              int64
	LastOutputAtMs              int64
	Loaded, Busy, StopRequested bool
	ReadOnly                    bool
	Resumed                     bool
	Messages                    []Message
	// Queue waits for the running turn to end.
	Queue []queuedMessage `json:"queue,omitempty"`
}
