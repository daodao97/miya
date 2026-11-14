package client

import (
	"miya/internal/chat/model"
)

func (m *Client) openCollaborationThread(raw string) {
	ref, ok := model.ParseThreadURL(raw)
	if !ok || ref.Source != m.State.ReferenceSource {
		m.SendErr = tr("Conversation unavailable")
		return
	}
	if thread, _, found := m.referencedThread(ref); found {
		m.OpenThread(thread)
		return
	}
	m.SendErr = tr("Conversation unavailable")
}

func (m *Client) referencedThread(ref model.ThreadRef) (model.Thread, model.ProjectSnapshot, bool) {
	if !ref.Valid() || ref.Source != m.State.ReferenceSource {
		return model.Thread{}, model.ProjectSnapshot{}, false
	}
	for _, project := range m.allPhoneProjects() {
		for _, thread := range project.Threads {
			if model.ThreadAgent(thread) == ref.Backend && thread.ServerID == ref.SessionID {
				return thread, project, true
			}
		}
	}
	return model.Thread{}, model.ProjectSnapshot{}, false
}
