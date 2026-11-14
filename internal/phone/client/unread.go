package client

import (
	"miya/internal/chat/model"
	"miya/internal/notification"
	"miya/internal/remote"
)

func (m *Client) unreadFor(t model.Thread) (notification.Unread, bool) {
	for _, u := range m.State.Notifications.Unread {
		if u.Matches(t) {
			return u, true
		}
	}
	return notification.Unread{}, false
}

func (m *Client) ApplyNotifications(n notification.PhoneState) {
	if n.Unread == nil {
		return
	}
	if n.Revision < m.BadgeRevision {
		return
	}
	m.BadgeRevision = n.Revision
	m.State.Notifications = n
	for key, version := range m.readPending {
		found := false
		for _, u := range n.Unread {
			if u.Key == key && u.Version <= version {
				found = true
			}
		}
		if !found {
			delete(m.readPending, key)
		}
	}

	m.markOpenRead()
}

func (m *Client) markOpenRead() {
	if m.Open == 0 || !m.CanSend() || m.background {
		return
	}
	t := m.Viewer.Current()
	u, ok := m.unreadFor(t)
	if !ok {
		return
	}
	if m.readPending[u.Key] >= u.Version {
		return
	}
	if m.readPending == nil {
		m.readPending = map[string]uint64{}
	}
	m.readPending[u.Key] = u.Version
	r := m.ThreadRequest("read", m.Open)
	r.ReadVersion = u.Version
	conn, gen := m.Conn, m.Generation
	go func() {
		if err := m.write(conn, r); err != nil {
			m.onMain(gen, func() { delete(m.readPending, u.Key) })
		}
	}()
}

func (m *Client) markCompletionRead() {
	if m.Open == 0 || !m.CanSend() || m.background {
		return
	}
	r, read, ok := m.CompletionReadRequest()
	if !ok {
		return
	}
	m.CompletionRead = read
	conn, gen := m.Conn, m.Generation
	go func() {
		if err := m.write(conn, r); err != nil {
			m.onMain(gen, func() {
				if m.Conn == conn && m.CompletionRead == read {
					m.CompletionRead = notification.DesktopRead{}
				}
			})
		}
	}()
}

func (m *Client) CompletionReadRequest() (remote.Request, notification.DesktopRead, bool) {
	t := m.Viewer.Current()
	read := m.completionVersion(t)
	if m.Open == 0 || t.ID != m.Open || t.Busy || t.Loading || !t.HistoryLoaded || len(t.Messages) == 0 || read.Version <= 0 {
		return remote.Request{}, notification.DesktopRead{}, false
	}
	if m.CompletionReceived != read || m.CompletionRead == read {
		return remote.Request{}, notification.DesktopRead{}, false
	}
	r := m.ThreadRequest("read", m.Open)
	r.ReadCompletedAtMs = read.Version
	return r, read, true
}

func (m *Client) completionVersion(t model.Thread) notification.DesktopRead {
	key := notification.DesktopUnreadKey(t)
	if m.State.CompletionUnread != nil {
		return notification.DesktopRead{Key: key, Version: m.State.CompletionUnread[key]}
	}
	return notification.DesktopRead{Key: key, Version: t.CompletedAtMs}
}

func (m *Client) markAllRead() {
	if !m.CanSend() {
		m.SendErr = tr("Not connected to desktop")
		return
	}
	m.sendPhoneAction(remote.Request{Op: "read-all", ReadVersion: m.State.Notifications.Revision})
}
