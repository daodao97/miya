package main

import (
	"encoding/json"
	"github.com/egoist/mygo"
	"log"
	"miya/internal/remote"
)

func (m *mobileApp) setupPush() {
	mygo.App.OnPushToken(func(token string) {
		log.Print("miya: APNs device registration succeeded")
		m.pushToken = token
		m.pushError = ""
		m.syncPush()
	})
	mygo.App.OnPushRegistrationError(func(err error) {
		log.Printf("miya: APNs registration failed: %v", err)
		m.pushError = tr("Unable to register for notifications. Check system notification settings.")
		invalidate()
	})
	mygo.App.SetNotificationPresentationHandler(func(e mygo.NotificationEvent) mygo.NotificationPresentation {
		if m.pushDisabled {
			return 0
		}
		if len(m.desktops.Items) > 1 {
			return mygo.PresentNotificationDefault
		}
		target := notificationTarget(e.Data)
		current := m.view.c.current()
		viewing := target.Thread != 0 && target.Thread == m.open
		if target.ServerID != "" {
			viewing = m.open != 0 && current.ServerID == target.ServerID && (target.Agent == "" || target.Agent == threadAgent(current))
		}
		if viewing {
			return 0
		}
		return mygo.PresentNotificationDefault
	})
	mygo.App.OnNotification(func(e mygo.NotificationEvent) {
		if e.Clicked {
			m.openNotification(e.Data)
		}
	})
}
func (m *mobileApp) requestPush() {
	if m.win == nil || !m.loaded || mygo.App.Lifecycle() != mygo.LifecycleActive || m.pushDisabled || m.pushRequesting {
		return
	}
	m.pushRequesting = true
	go func() {

		status, e := mygo.Permissions.Request(mygo.PermissionNotifications)
		log.Printf("miya: notification permission: %v, error: %v", status, e)
		runOnMain(func() {
			m.pushRequesting = false
			if e != nil || status == mygo.PermissionDenied {
				m.pushError = tr("Allow Miya notifications in Settings")
				invalidate()
				return
			}
			if e := mygo.App.RegisterPushNotifications(); e != nil {
				m.pushError = tr("Unable to register notifications: ") + e.Error()
			}
			m.syncPush()
		})
	}()
}
func (m *mobileApp) syncPush() {
	if m.conn == nil || m.pushID == "" || (!m.pushDisabled && m.pushToken == "") {
		return
	}
	b, _ := json.Marshal(pushRegistration{ID: m.pushID, Token: m.pushToken, Disabled: m.pushDisabled})
	conn := m.conn
	go conn.Send(remote.Request{Op: "push", Push: b})
}
func (m *mobileApp) openPushThread() {
	if m.pushOpen == 0 && m.pushOpenRef.ServerID == "" {
		return
	}
	for _, p := range m.state.Projects {
		for _, t := range p.Threads {
			match := t.ID == m.pushOpen
			if m.pushOpenRef.ServerID != "" {
				match = t.ServerID == m.pushOpenRef.ServerID && (m.pushOpenRef.Agent == "" || threadAgent(t) == m.pushOpenRef.Agent) && (m.pushOpenRef.Project == "" || p.Path == m.pushOpenRef.Project)
			}
			if match {
				m.pushOpen = 0
				m.pushOpenRef = remote.Request{}
				m.openThread(t)
				return
			}
		}
	}
}
