package client

import (
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/notification"
)

func (m *Client) StartConversation(project, agent string) {
	if project == "" {
		project = m.selectedProject().Path
	}
	m.Home()
	m.Menu = false

	m.Selected, m.NewAgent, m.Title = project, agent, ""
	if project != "" {
		m.ExpandedProject = project
	}
}

func (m *Client) SidebarActivityThreads() (running, completed []model.Thread) {
	return msgfmt.SidebarActivityThreads(m.allPhoneProjects(), m.completionUnread)
}

func (m *Client) completionUnread(thread model.Thread) bool {
	if m.State.CompletionUnread != nil {
		return m.State.CompletionUnread[notification.DesktopUnreadKey(thread)] > 0
	}
	_, unread := m.unreadFor(thread)
	return unread
}
