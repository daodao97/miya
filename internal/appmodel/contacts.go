package appmodel

import (
	"miya/internal/chat/model"
	"miya/internal/contacts"
)

type ContactResult struct {
	ProfileID string            `json:"profileId,omitempty"`
	RequestID string            `json:"requestId,omitempty"`
	State     contacts.Snapshot `json:"state"`
	Thread    model.Thread      `json:"thread,omitempty"`
	Workspace string            `json:"workspace,omitempty"`
	Avatar    []byte            `json:"avatar,omitempty"`
}
