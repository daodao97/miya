// Package interaction is the shared contract for approvals, questions and forms.
// Legacy wire fields remain available to adapters; views use Kind and typed responses.
package interaction

import (
	"encoding/json"
	"strings"

	agentmeta "miya/internal/agent/catalog"
)

type Request struct {
	Agent           string          `json:"agent,omitempty"`
	Category        Kind            `json:"kind,omitempty"`
	Permissions     json.RawMessage `json:"permissions"`
	Payload         json.RawMessage
	Questions       []Question      `json:"questions"`
	Message         string          `json:"message"`
	Mode            string          `json:"mode"`
	URL             string          `json:"url"`
	ServerName      string          `json:"serverName"`
	RequestedSchema json.RawMessage `json:"requestedSchema"`
	ID              json.RawMessage
	Method          string
	ThreadID        string `json:"threadId"`
	TurnID          string `json:"turnId"`
	ItemID          string `json:"itemId"`
	Command         string `json:"command"`
	Cwd             string `json:"cwd"`
	Reason          string `json:"reason"`
	GrantRoot       string `json:"grantRoot"`
}

type Question struct {
	ID       string `json:"id"`
	Header   string `json:"header"`
	Question string `json:"question"`
	IsSecret bool   `json:"isSecret"`
	Options  []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
}

// HasOption reports whether label is one of the question's offered answers,
// as opposed to one the person typed.
func (q Question) HasOption(label string) bool {
	for _, o := range q.Options {
		if o.Label == label {
			return true
		}
	}
	return false
}

type Kind string

const (
	Approval       Kind = "approval"
	Questions      Kind = "questions"
	Form           Kind = "form"
	DesktopControl Kind = "desktop-control"
)

// Kind classifies the established wire envelope at the compatibility boundary.
// A provider may explicitly set Category without adopting any native method names.
func (r Request) Kind() Kind {
	if r.Category != "" {
		return r.Category
	}
	switch r.Method {
	case "item/tool/requestUserInput":
		return Questions
	case "mcpServer/elicitation/request":
		return Form
	case "miya/desktopControl":
		return DesktopControl
	default:
		return Approval
	}
}

func (r Request) Owner() string {
	if r.Agent != "" {
		return r.Agent
	}
	// Older persisted requests and older services identify their producer in the ID.
	if strings.Contains(string(r.ID), "\"miya:") {
		return "miya"
	}
	if strings.Contains(string(r.ID), "claude:") {
		return agentmeta.Claude
	}
	return agentmeta.Default().ID
}
func (r Request) AgentName() string {
	if r.Owner() == "miya" {
		return "Miya"
	}
	if d, ok := agentmeta.Lookup(r.Owner()); ok {
		return d.Name
	}
	return r.Owner()
}

type Action string

const (
	Allow   Action = "allow"
	Decline Action = "decline"
	Cancel  Action = "cancel"
	Submit  Action = "submit"
	Skip    Action = "skip"
)

// Response expresses user intent; adapters encode it into their native protocol.
type Response struct {
	Action  Action            `json:"action"`
	Answers map[string]string `json:"answers,omitempty"`
	// An empty object confirms a form with no fields; nil means no form
	// was submitted. Preserve that distinction across the phone transport.
	Content map[string]any `json:"content"`
}
