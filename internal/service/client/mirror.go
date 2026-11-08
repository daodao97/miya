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

// ResetForSource drops cached drafts and messages when the service changed.
func (m *Mirror) ResetForSource(sameSource bool) {
	if !sameSource {
		m.Drafts = map[int]string{}
		m.Messages = map[int][]model.Message{}
	}
}
