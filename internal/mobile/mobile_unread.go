package mobile

import (
	appmodel "miya/internal/appmodel"
	notification "miya/internal/notification"
	remote "miya/internal/remote"
	uiruntime "miya/internal/uiruntime"
	strconv "strconv"

	mygo "github.com/egoist/mygo"
)

func (m *App) unreadFor(t appmodel.Thread) (notification.Unread, bool) {
	for _, u := range m.State.Notifications.Unread {
		if u.Matches(t) {
			return u, true
		}
	}
	return notification.Unread{}, false
}

func (m *App) ApplyNotifications(n notification.PhoneState) {
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
	m.updateBadge()
	m.markOpenRead()
}

func (m *App) markOpenRead() {
	if m.Open == 0 || m.Conn == nil || m.win != nil && mygo.App.Lifecycle() != mygo.LifecycleActive {
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
		if err := conn.Send(r); err != nil {
			m.onMain(gen, func() { delete(m.readPending, u.Key) })
		}
	}()
}

func (m *App) markCompletionRead() {
	if m.Open == 0 || m.Conn == nil || m.win != nil && mygo.App.Lifecycle() != mygo.LifecycleActive {
		return
	}
	r, read, ok := m.CompletionReadRequest()
	if !ok {
		return
	}
	m.CompletionRead = read
	conn, gen := m.Conn, m.Generation
	go func() {
		if err := conn.Send(r); err != nil {
			m.onMain(gen, func() {
				if m.Conn == conn && m.CompletionRead == read {
					m.CompletionRead = appmodel.DesktopRead{}
				}
			})
		}
	}()
}

func (m *App) CompletionReadRequest() (remote.Request, appmodel.DesktopRead, bool) {
	t := m.Viewer.Current()
	if m.Open == 0 || t.ID != m.Open || t.Busy || t.Loading || !t.HistoryLoaded || len(t.Messages) == 0 || t.CompletedAtMs <= 0 {
		return remote.Request{}, appmodel.DesktopRead{}, false
	}
	read := appmodel.DesktopRead{Key: appmodel.DesktopUnreadKey(t), Version: t.CompletedAtMs}
	if m.CompletionReceived != read || m.CompletionRead == read {
		return remote.Request{}, appmodel.DesktopRead{}, false
	}
	r := m.ThreadRequest("read", m.Open)
	r.ReadCompletedAtMs = read.Version
	return r, read, true
}

func (m *App) DesiredBadge() int {
	if m.PushDisabled {
		return 0
	}
	return len(m.State.Notifications.Unread)
}

func (m *App) updateBadge() {
	count := m.DesiredBadge()
	if m.win == nil || m.badgeUpdating || m.badgeApplied == count {
		return
	}
	m.badgeUpdating = true
	go func() {
		err := mygo.App.SetNotificationBadge(count)
		uiruntime.RunOnMain(func() {
			m.badgeUpdating = false
			if err == nil {
				m.badgeApplied = count
				if m.DesiredBadge() != count {
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

func (m *App) markAllRead() {
	if m.Conn == nil {
		m.SendErr = tr("Not connected to desktop")
		return
	}
	m.sendPhoneAction(remote.Request{Op: "read-all", ReadVersion: m.State.Notifications.Revision})
}
