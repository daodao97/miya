// Package model holds the conversation data the window, the service and
// phones share: threads, messages and their parts.
package model

import (
	"miya/internal/agent/codex/native"
	"miya/internal/tokenusage"
)

type Message struct {
	Presentation   *MessagePresentation `json:"presentation,omitempty"`
	ExecutionID    string               `json:"executionId,omitempty"`
	MemoryControl  bool                 `json:"memoryControl,omitempty"`
	MemoryTurnID   string               `json:"memoryTurnId,omitempty"`
	Memory         *MemoryNotice        `json:"memory,omitempty"`
	HandoffTitle   string               `json:"handoffTitle,omitempty"`
	Parts          []MessagePart        `json:"parts,omitempty"`
	Images         []string
	Attachments    []string `json:"attachments,omitempty"`
	Kind           string
	Status         string
	Item           *codex.Item
	DiffStats      *DiffLineStats `json:",omitempty"`
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
	ExecutionID          string `json:"executionId,omitempty"`
	LaunchModel          string `json:"launchModel,omitempty"`
	LaunchEffort         string `json:"launchEffort,omitempty"`
	ClaudeGeneration     uint64 `json:"-"`
	MemoryDisabled       bool   `json:"memoryDisabled,omitempty"`
	MemoryEpoch          uint64 `json:"memoryEpoch,omitempty"`
	CompletionNoticeBody string `json:"completionNoticeBody,omitempty"`
	Agent                string
	Tasks                []ThreadTask `json:",omitempty"`
	TaskPending          bool         `json:",omitempty"`
	TaskStopping         bool         `json:",omitempty"`
	GoalRevision         uint64       `json:"-"`
	// Automation is the automation that started the conversation.
	Automation                  string `json:",omitempty"`
	ClaudeMessageID             string
	Usage                       *tokenusage.Usage
	TurnOutputTokens            *int           `json:",omitempty"`
	TurnOutputBaseline          *int           `json:"-"`
	ClaudeOutputTokens          map[string]int `json:"-"`
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
	Queue []QueuedMessage `json:"queue,omitempty"`
}
