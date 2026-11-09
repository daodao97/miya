package mobile

import (
	"encoding/json"
	"log"
	"runtime"

	"github.com/egoist/mygo"

	"miya/internal/chat/model"
	"miya/internal/notification"
	"miya/internal/remote"
	"miya/internal/uiruntime"
)

func (m *App) setupPush() {
	mygo.App.OnPushToken(func(token string) {
		log.Print("miya: APNs device registration succeeded")
		m.pushToken = token
		m.pushError = ""
		m.syncPush()
	})
	mygo.App.OnPushRegistrationError(func(err error) {
		log.Printf("miya: APNs registration failed: %v", err)
		m.pushError = tr("Unable to register for notifications. Check system notification settings.")
		uiruntime.Invalidate()
	})
	mygo.App.SetNotificationPresentationHandler(func(e mygo.NotificationEvent) mygo.NotificationPresentation {
		if !m.ShouldPresentNotification(e.Data) {
			return 0
		}
		return mygo.PresentNotificationDefault
	})
	mygo.App.OnNotification(func(e mygo.NotificationEvent) {
		if e.Clicked {
			m.OpenNotification(e.Data)
		}
	})
}

// ShouldPresentNotification reports whether a notification arriving while
// the app is in the foreground should be shown: not for the conversation
// already on screen. With several desktops, only a notification naming the
// connected desktop can be matched against the open conversation.
func (m *App) ShouldPresentNotification(data map[string]string) bool {
	if m.PushDisabled {
		return false
	}
	if len(m.Desktops.Items) > 1 && (data["desktop"] == "" || data["desktop"] != m.CurrentDesktopID()) {
		return true
	}
	target := notificationTarget(data)
	current := m.Viewer.Current()
	viewing := target.Thread != 0 && target.Thread == m.Open
	if target.ServerID != "" {
		viewing = m.Open != 0 && current.ServerID == target.ServerID && (target.Agent == "" || target.Agent == model.ThreadAgent(current))
	}
	return !viewing
}

func (m *App) requestPush() {
	if m.win == nil || !m.Loaded || mygo.App.Lifecycle() != mygo.LifecycleActive || m.PushDisabled || m.pushRequesting {
		return
	}
	m.pushRequesting = true
	go func() {

		status, e := mygo.Permissions.Query(mygo.PermissionNotifications)
		// Returning from a denied system prompt activates the app again. Only
		// the first request should prompt; later denials belong in Settings.
		if e == nil && status == mygo.PermissionNotDetermined {
			status, e = mygo.Permissions.Request(mygo.PermissionNotifications)
		}
		log.Printf("miya: notification permission: %v, error: %v", status, e)
		uiruntime.RunOnMain(func() {
			m.pushRequesting = false
			if e != nil || status == mygo.PermissionDenied {
				m.pushError = tr("Allow Miya notifications in Settings")
				uiruntime.Invalidate()
				return
			}
			if runtime.GOOS == "android" {
				// The paired connection delivers notifications itself.
				m.pushLive, m.pushError = true, ""
			} else if e := mygo.App.RegisterPushNotifications(); e != nil {
				m.pushError = tr("Unable to register notifications: ") + e.Error()
			}
			m.syncPush()
		})
	}()
}

func (m *App) syncPush() {
	m.updateKeepRunning()
	if m.Conn == nil || m.pushID == "" || (!m.PushDisabled && m.pushToken == "" && !m.pushLive) {
		return
	}
	b, _ := json.Marshal(notification.Registration{ID: m.pushID, Token: m.pushToken, Disabled: m.PushDisabled, Desktop: m.CurrentDesktopID(), Live: m.pushLive})
	conn := m.Conn
	go conn.Send(remote.Request{Op: "push", Push: b})
}

func (m *App) openPushThread() {
	if m.pushOpen == 0 && m.pushOpenRef.ServerID == "" {
		return
	}
	for _, p := range m.allPhoneProjects() {
		for _, t := range p.Threads {
			match := t.ID == m.pushOpen
			if m.pushOpenRef.ServerID != "" {
				match = t.ServerID == m.pushOpenRef.ServerID && (m.pushOpenRef.Agent == "" || model.ThreadAgent(t) == m.pushOpenRef.Agent) && (m.pushOpenRef.Project == "" || p.Path == m.pushOpenRef.Project)
			}
			if match {
				m.pushOpen = 0
				m.pushOpenRef = remote.Request{}
				log.Printf("miya: notification opens conversation %d", t.ID)
				m.OpenThread(t)
				return
			}
		}
	}
	log.Printf("miya: notification conversation %d (server %q) not in the list yet", m.pushOpen, m.pushOpenRef.ServerID)
}

// updateKeepRunning holds mygo.Power.KeepRunning while Android receives
// notifications over the paired connection. Android starts the foreground
// service only for an app on screen, so it is acquired in the foreground and
// kept through the background. A process the system ends anyway stays ended.
func (m *App) updateKeepRunning() {
	want := m.pushLive && !m.PushDisabled && m.Link != ""
	if want && m.keepRunning == nil && !m.background {
		m.keepRunning = mygo.Power.KeepRunning(tr("Receiving notifications from your Mac"))
	} else if !want && m.keepRunning != nil {
		m.keepRunning()
		m.keepRunning = nil
	}
}

// showLiveNotice shows a notification the desktop sent over the paired
// connection (Android), unless it names the conversation on screen.
func (m *App) showLiveNotice(n notification.LiveMessage) {
	if m.PushDisabled {
		return
	}
	if n.Silent {
		// Everything was read: take back what is shown.
		if n.Badge != nil && *n.Badge == 0 {
			for id, shown := range m.liveShown {
				shown.Close()
				delete(m.liveShown, id)
			}
		}
		return
	}
	if !m.ShouldPresentNotification(n.Data) {
		return
	}
	if m.liveShown == nil {
		m.liveShown = map[string]*mygo.Notification{}
	}
	if old := m.liveShown[n.ID]; old != nil {
		old.Close()
	}
	id, data := n.ID, n.Data
	shown := mygo.NewNotification(mygo.NotificationOptions{ID: id, Title: tr(n.Title), Body: n.Body, Group: n.Group, Data: data})
	shown.OnClick(func() {
		log.Printf("miya: notification %s clicked: thread=%s server=%q", id, data["thread"], data["serverId"])
		delete(m.liveShown, id)
		m.OpenNotification(data)
	})
	if err := shown.Show(); err != nil {
		log.Printf("miya: notification: %v", err)
		shown.Close()
		return
	}
	m.liveShown[id] = shown
}
