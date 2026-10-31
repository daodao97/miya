package main

import "miya/internal/codex"

type Message struct {
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
}

type Thread struct {
	Agent                       string
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
