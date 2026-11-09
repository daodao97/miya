package mobile

import (
	"encoding/json"
	"log"

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
			if e := mygo.App.RegisterPushNotifications(); e != nil {
				m.pushError = tr("Unable to register notifications: ") + e.Error()
			}
			m.syncPush()
		})
	}()
}

func (m *App) syncPush() {
	if m.Conn == nil || m.pushID == "" || (!m.PushDisabled && m.pushToken == "") {
		return
	}
	b, _ := json.Marshal(notification.Registration{ID: m.pushID, Token: m.pushToken, Disabled: m.PushDisabled, Desktop: m.CurrentDesktopID()})
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
				m.OpenThread(t)
				return
			}
		}
	}
}
