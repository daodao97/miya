package client

import (
	"miya/internal/chat/model"
	svcproto "miya/internal/service/protocol"
)

type Mirror struct {
	RemoteHost  bool
	ProjectBusy map[string]bool
	Messages    map[int][]model.Message
	// Draft edits belong to this window, including explicit clears after a
	// send. Older service snapshots must not replace them during navigation.
	Drafts    map[int]string
	SetActive func(int)
	SetEffort func(string)
	SetPolicy func(string)
	SetLocale func(string)
	// reconnect connects again after the service went away; nil does not.
	Reconnect func() (*Client, svcproto.State, error)
	// phone is the service's phone connection, for the settings.
	Phone svcproto.PhoneStatus
}

// RemapContacts preserves contact-local edits across service renumbering.
// Ordinary native sessions retain their existing cache lifecycle: a native
// rewind can change their session ID while retaining earlier message IDs.
func (m *Mirror) RemapContacts(previous, incoming []model.Thread, sameSource bool) {
	if !sameSource {
		m.Drafts = map[int]string{}
		m.Messages = map[int][]model.Message{}
		return
	}
	drafts := map[string]string{}
	messages := map[string][]model.Message{}
	for _, t := range previous {
		if t.Contact == nil {
			continue
		}
		if v, ok := m.Drafts[t.ID]; ok {
			drafts[t.Contact.ID] = v
		}
		if v, ok := m.Messages[t.ID]; ok {
			messages[t.Contact.ID] = v
		}
		delete(m.Drafts, t.ID)
		delete(m.Messages, t.ID)
	}
	if m.Drafts == nil {
		m.Drafts = map[int]string{}
	}
	if m.Messages == nil {
		m.Messages = map[int][]model.Message{}
	}
	for _, t := range incoming {
		if t.Contact == nil {
			continue
		}
		// A new contact number may previously have belonged to another session.
		delete(m.Drafts, t.ID)
		delete(m.Messages, t.ID)
		if v, ok := drafts[t.Contact.ID]; ok {
			m.Drafts[t.ID] = v
		}
		if v, ok := messages[t.Contact.ID]; ok {
			m.Messages[t.ID] = v
		}
	}
}
