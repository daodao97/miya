// Package protocol is the data the background service and the phone pass in
// the Data of remote frames and requests. Field names and JSON tags are the
// wire format: changing them breaks phones and desktops of other versions.
package protocol

import (
	"encoding/json"

	"miya/internal/agentmeta"
	"miya/internal/automation"
	"miya/internal/interaction"
	"miya/internal/memory/manager"
	"miya/internal/model"
	"miya/internal/notification"
	"miya/internal/remote"
)

type State struct {
	Collaboration    *model.CollaborationStatus `json:"collaboration,omitempty"`
	DesktopWindows   bool                       `json:"desktopWindows,omitempty"`
	DesktopControl   bool                       `json:"desktopControl,omitempty"`
	MemoryRevision   uint64                     `json:"memoryRevision,omitempty"`
	Memory           bool                       `json:"memory,omitempty"`
	ReferenceSource  string                     `json:"referenceSource"`
	Files            bool                       `json:"files,omitempty"`
	WebPorts         bool                       `json:"webPorts,omitempty"`
	Runtime          bool                       `json:"runtime,omitempty"`
	WebServices      bool                       `json:"webServices,omitempty"`
	FileReferences   bool                       `json:"fileReferences,omitempty"`
	Automations      bool                       `json:"automations,omitempty"`
	AutomationRun    bool                       `json:"automationRun,omitempty"`
	AutomationCreate bool                       `json:"automationCreate,omitempty"`
	Projects         []model.ProjectSnapshot    `json:"projects"`
	Status           string                     `json:"status"`
	Approvals        []interaction.Request      `json:"approvals"`
	Agents           []AgentOptions             `json:"agents,omitempty"`
	Quotas           []AgentQuota               `json:"quotas,omitempty"`
	Notifications    notification.PhoneState    `json:"notifications"`
	// Agent, Model and Effort are the desktop's defaults for new messages.
	Agent  string `json:"agent"`
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

// ThreadDelta brings the phone's copy of a conversation up to date:
// its metadata, the order of all its messages, and the messages the phone
// does not have as they are now. Reset drops what the phone had first.
type ThreadDelta struct {
	Thread  model.Thread    `json:"thread"`
	Order   []int           `json:"order"`
	Changed []model.Message `json:"changed,omitempty"`
	Reset   bool            `json:"reset,omitempty"`
}

type ApprovalReply struct {
	ID json.RawMessage `json:"id"`
}

type FormField struct {
	Type        string            `json:"type"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Enum        []json.RawMessage `json:"enum"`
}

type FormSchema struct {
	Type       string               `json:"type"`
	Required   []string             `json:"required"`
	Properties map[string]FormField `json:"properties"`
}

type ModelOption struct {
	Value         string   `json:"value"`
	Label         string   `json:"label"`
	Efforts       []string `json:"efforts"`
	DefaultEffort string   `json:"defaultEffort"`
}

type AgentOptions struct {
	Commands   []model.ComposerChoice `json:"commands,omitempty"`
	Descriptor agentmeta.Descriptor   `json:"descriptor,omitempty"`
	Agent      string                 `json:"agent"`
	Current    remote.SendOptions     `json:"current"`
	Models     []ModelOption          `json:"models"`
}

// Only subscription usage is shared, never account identity or credentials.
// Agents without real quota data are omitted entirely.
type AgentQuota struct {
	Agent   string        `json:"agent"`
	Windows []QuotaWindow `json:"windows"`
}

type QuotaWindow struct {
	Label       string  `json:"label"`
	UsedPercent float64 `json:"usedPercent"`
	ResetsAt    int64   `json:"resetsAt,omitempty"`
}

type MemoryResult struct {
	Revision uint64       `json:"revision,omitempty"`
	Project  string       `json:"project"`
	Scope    string       `json:"scope"`
	List     manager.List `json:"list"`
}

type AutomationRunResult struct {
	RequestID string            `json:"requestId"`
	List      []automation.View `json:"list"`
}

type AutomationFormResult struct {
	RequestID string           `json:"requestId"`
	View      *automation.View `json:"view,omitempty"`
}

type DesktopReply struct {
	RequestID string `json:"requestId"`
	Token     string `json:"token,omitempty"`
}

type CollaborationResult struct {
	RequestID string `json:"request_id"`
	Result    any    `json:"result,omitempty"`
}

// Commands is a project and agent's command catalog. Untagged messages are from legacy servers.
type Commands struct {
	Agent    string                 `json:"agent,omitempty"`
	Project  string                 `json:"project"`
	Commands []model.ComposerChoice `json:"commands"`
}
