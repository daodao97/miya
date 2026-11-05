package main

import (
	"strconv"

	"github.com/egoist/mygo"
	"miya/internal/remote"
	"miya/internal/textutil"
)

func unreadMatches(u phoneUnread, t Thread) bool {
	if u.ServerID != "" {
		return u.ServerID == t.ServerID && textutil.FirstNonEmpty(u.Agent, agentCodex) == threadAgent(t)
	}
	return u.Thread == strconv.Itoa(t.ID)
}
func (m *mobileApp) unreadFor(t Thread) (phoneUnread, bool) {
	for _, u := range m.state.Notifications.Unread {
		if unreadMatches(u, t) {
			return u, true
		}
	}
	return phoneUnread{}, false
}
func (m *mobileApp) applyNotifications(n phoneNotificationState) {
	if n.Unread == nil {
		return
	}
	if n.Revision < m.badgeRevision {
		return
	}
	m.badgeRevision = n.Revision
	m.state.Notifications = n
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
	m.updateBadge()
	m.markOpenRead()
}
func (m *mobileApp) markOpenRead() {
	if m.open == 0 || m.conn == nil || m.win != nil && mygo.App.Lifecycle() != mygo.LifecycleActive {
		return
	}
	t := m.view.c.current()
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
	r := m.threadRequest("read", m.open)
	r.ReadVersion = u.Version
	conn, gen := m.conn, m.generation
	go func() {
		if err := conn.Send(r); err != nil {
			m.onMain(gen, func() { delete(m.readPending, u.Key) })
		}
	}()
}

func (m *mobileApp) markCompletionRead() {
	if m.open == 0 || m.conn == nil || m.win != nil && mygo.App.Lifecycle() != mygo.LifecycleActive {
		return
	}
	r, read, ok := m.completionReadRequest()
	if !ok {
		return
	}
	m.completionRead = read
	conn, gen := m.conn, m.generation
	go func() {
		if err := conn.Send(r); err != nil {
			m.onMain(gen, func() {
				if m.conn == conn && m.completionRead == read {
					m.completionRead = desktopRead{}
				}
			})
		}
	}()
}

func (m *mobileApp) completionReadRequest() (remote.Request, desktopRead, bool) {
	t := m.view.c.current()
	if m.open == 0 || t.ID != m.open || t.Busy || t.Loading || !t.HistoryLoaded || len(t.Messages) == 0 || t.CompletedAtMs <= 0 {
		return remote.Request{}, desktopRead{}, false
	}
	read := desktopRead{Key: desktopUnreadKey(t), Version: t.CompletedAtMs}
	if m.completionReceived != read || m.completionRead == read {
		return remote.Request{}, desktopRead{}, false
	}
	r := m.threadRequest("read", m.open)
	r.ReadCompletedAtMs = read.Version
	return r, read, true
}
func (m *mobileApp) desiredBadge() int {
	if m.pushDisabled {
		return 0
	}
	return len(m.state.Notifications.Unread)
}
func (m *mobileApp) updateBadge() {
	count := m.desiredBadge()
	if m.win == nil || m.badgeUpdating || m.badgeApplied == count {
		return
	}
	m.badgeUpdating = true
	go func() {
		err := mygo.App.SetNotificationBadge(count)
		runOnMain(func() {
			m.badgeUpdating = false
			if err == nil {
				m.badgeApplied = count
				if m.desiredBadge() != count {
					m.updateBadge()
				}
			}
		})
	}()
}
func notificationTarget(data map[string]string) remote.Request {
	id, _ := strconv.Atoi(data["thread"])
	return remote.Request{Thread: id, ServerID: data["serverId"], Agent: data["agent"], Project: data["project"]}
}

func (m *mobileApp) markAllRead() {
	if m.conn == nil {
		m.sendErr = tr("Not connected to desktop")
		return
	}
	m.sendPhoneAction(remote.Request{Op: "read-all", ReadVersion: m.state.Notifications.Revision})
}
