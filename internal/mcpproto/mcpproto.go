// Package mcpproto is what the -mcp process and the background service pass
// for a tool call: the request, its results and the tools' descriptions.
// Field names and JSON tags are the wire format between versions.
package mcpproto

import (
	"encoding/json"

	"miya/internal/automation"
)

// CallerUnknown is the service's answer while the calling conversation
// has not shown its tool call yet; the call is tried again shortly.
const CallerUnknown = "Could not find the calling conversation"

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

var ReadOnly = map[string]any{"readOnlyHint": true, "openWorldHint": false}

// Request is a tool call as the service receives it.
type Request struct {
	TaskID    string `json:"taskId,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	// Tool is the miya tool being called, by which the service finds the
	// conversation calling it.
	Tool        string `json:"tool,omitempty"`
	WindowApp   string `json:"windowApp,omitempty"`
	WindowTitle string `json:"windowTitle,omitempty"`
	Thread      int    `json:"thread,omitempty"`
	Reference   string `json:"reference,omitempty"`
	Agent       string `json:"agent,omitempty"`
	Task        string `json:"task,omitempty"`
	NoContext   bool   `json:"noContext,omitempty"`
	// Arguments preserves a memory tool's original parameters for caller matching.
	Arguments json.RawMessage `json:"arguments,omitempty"`
	// Auto is an automation tool's arguments.
	Auto *AutomationArgs `json:"auto,omitempty"`
	// Report is report_result\'s result.
	Report *automation.Report `json:"report,omitempty"`
}

type ThreadInfo struct {
	Reference string `json:"reference"`
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Agent     string `json:"agent"`
	Status    string `json:"status"`
}

type ThreadsResult struct {
	Threads []ThreadInfo `json:"threads"`
}

type ReadResult struct {
	Loaded bool   `json:"loaded"`
	Packet string `json:"packet"`
}

type StatusResult struct {
	Reference string `json:"reference"`
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Agent     string `json:"agent"`
	Status    string `json:"status"`
	Reply     string `json:"reply"`
}

type DelegateResult struct {
	TaskID    string `json:"taskId,omitempty"`
	Reference string `json:"reference"`
	Thread    int    `json:"thread"`
	Agent     string `json:"agent"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type AutomationArgs struct {
	ID           string `json:"id,omitempty"`
	Name         string `json:"name,omitempty"`
	Schedule     string `json:"schedule,omitempty"`
	Instructions string `json:"instructions,omitempty"`
	Check        string `json:"check,omitempty"`
	Rounds       int    `json:"rounds,omitempty"`
	Agent        string `json:"agent,omitempty"`
	Enabled      *bool  `json:"enabled,omitempty"`
}
