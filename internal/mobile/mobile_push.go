package mobile

import (
	"context"
	"encoding/json"
	"log"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/permissionflow"
	"github.com/egoist/mygo/plugins/push"

	"miya/internal/chat/model"
	"miya/internal/notification"
	"miya/internal/remote"
	"miya/internal/uiruntime"
)

func (m *App) setupPush() {
	push.OnToken(func(token string) {
		log.Print("miya: APNs device registration succeeded")
		m.pushToken = token
		m.pushError = ""
		m.syncPush()
	})
	push.SetPresentation(func(data []byte) bool { return m.ShouldPresentNotification(notificationData(data)) })
	// A tapped push (APNs), also the one that launched the app.
	push.OnOpened(func(data []byte) { m.OpenNotification(notificationData(data)) })
	// A tapped notification of Miya's own, also of an earlier run: its ID
	// carries what it opens (notificationID).
	mygo.App.OnNotificationClick(func(id string) {
		key, data, ok := parseNotificationID(id)
		if !ok {
			return
		}
		log.Printf("miya: notification %s clicked: thread=%s server=%q", key, data["thread"], data["serverId"])
		delete(m.liveShown, key)
		m.OpenNotification(data)
	})
}

// notificationData decodes what a push carries, as JSON.
func notificationData(data []byte) map[string]string {
	var d map[string]string
	_ = json.Unmarshal(data, &d)
	return d
}

// notificationID is the ID of a notification Miya shows, key, which carries
// data, what a tap opens: mygo.App.OnNotificationClick is told only the ID,
// also after the process was ended.
func notificationID(key string, data map[string]string) string {
	q := url.Values{}
	for k, v := range data {
		q.Set(k, v)
	}
	return key + "#" + q.Encode()
}

// parseNotificationID returns the key and data of a notificationID, and
// false for an ID of another kind, as a push's.
func parseNotificationID(id string) (key string, data map[string]string, ok bool) {
	key, query, ok := strings.Cut(id, "#")
	if !ok {
		return "", nil, false
	}
	q, err := url.ParseQuery(query)
	if err != nil {
		return "", nil, false
	}
	data = map[string]string{}
	for k := range q {
		data[k] = q.Get(k)
	}
	return key, data, true
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

		status, e := permissionflow.Check(permissionflow.Notifications)
		// Returning from a denied system prompt activates the app again. Only
		// the first request should prompt; later denials belong in Settings.
		if e == nil && status == permissionflow.NotDetermined {
			status, e = permissionflow.Request(context.Background(), permissionflow.Notifications)
		}
		log.Printf("miya: notification permission: %v, error: %v", status, e)
		uiruntime.RunOnMain(func() {
			m.pushRequesting = false
			if e != nil || status == permissionflow.Denied {
				m.pushError = tr("Allow Miya notifications in Settings")
				uiruntime.Invalidate()
				return
			}
			if runtime.GOOS == "android" {
				// The paired connection delivers notifications itself.
				m.pushLive, m.pushError = true, ""
			} else {
				go m.registerPush()
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
	// Taps reach OnNotificationClick (setupPush), which the ID tells what to
	// open.
	shown := mygo.NewNotification(mygo.NotificationOptions{ID: notificationID(id, data), Title: tr(n.Title), Body: n.Body, Group: n.Group, Data: data})
	if err := shown.Show(); err != nil {
		log.Printf("miya: notification: %v", err)
		shown.Close()
		return
	}
	m.liveShown[id] = shown
}

// registerPush asks APNs for the token pushes are sent to, which also
// reaches push.OnToken. Off the main goroutine: it waits for the system.
func (m *App) registerPush() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := push.Register(ctx); err != nil {
		log.Printf("miya: APNs registration failed: %v", err)
		uiruntime.RunOnMain(func() {
			m.pushError = tr("Unable to register for notifications. Check system notification settings.")
			uiruntime.Invalidate()
		})
	}
}
