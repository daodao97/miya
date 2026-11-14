package client

import "miya/internal/interaction"

// ApprovalsFor are the pending requests of the conversation with serverID,
// none for a conversation the desktop has not started yet.
func (m *Client) ApprovalsFor(serverID string) []interaction.Request {
	if serverID == "" {
		return nil
	}
	var out []interaction.Request
	for _, a := range m.State.Approvals {
		if a.ThreadID == serverID {
			out = append(out, a)
		}
	}
	return out
}

// ConversationApprovals are the requests the open conversation shows: its
// own, and those that belong to no conversation.
func (m *Client) ConversationApprovals() []interaction.Request {
	serverID := m.Viewer.Current().ServerID
	var out []interaction.Request
	for _, a := range m.State.Approvals {
		if a.ThreadID == "" || serverID != "" && a.ThreadID == serverID {
			out = append(out, a)
		}
	}
	return out
}
