package nsmobile

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/widget"
	"miya/internal/apns"
	"miya/internal/chat/model"
	"miya/internal/notification"
	"miya/internal/prefs"
	"miya/internal/remote"
	"miya/internal/service/workspaces"
)

type phonePush struct {
	activityUpdated                             time.Time
	pendingActivity                             model.ThreadRef
	activityDesktop                             string
	id, token, err, sent, owner, pendingDesktop string
	requestSeq                                  uint64
	closed                                      bool
	conn                                        *remote.Conn
	generation                                  int
	disabled, granted, requesting, activities   bool
	cancel                                      context.CancelFunc
	stopRemote, keepRunning                     func()
	pending                                     map[string]string
	shown                                       map[string]string
	activity                                    *widget.Activity
	activityOwner                               model.ThreadRef
	activityState                               string
}

func (m *App) desktopID() string { id, _ := workspaces.IDFromLink(m.Link); return id }
func (m *App) setupPush(cx *core.Scope) {
	m.push.id = prefs.Load("nsgo-notification-device")
	if decoded, err := hex.DecodeString(m.push.id); err != nil || len(decoded) != 16 {
		var b [16]byte
		if _, err := rand.Read(b[:]); err == nil {
			m.push.id = hex.EncodeToString(b[:])
			prefs.Save("nsgo-notification-device", m.push.id)
		}
	}
	m.push.disabled = prefs.Load("phone-notifications") == "off"
	m.push.activities = prefs.Load("nsgo-phone-live-activities") == "on"
	m.push.shown = map[string]string{}
	m.OnLiveNotification = m.showLiveNotice
	m.OnPushResult = func(message string) { m.push.err = message }
	cx.OnNotificationPress(func(id string) {
		if key, data, ok := notification.ParseID(id); ok {
			delete(m.push.shown, key)
			m.openNotification(data)
			m.change()
		}
	})
	if PlatformHooks.ObserveRemoteNotifications != nil {
		m.push.stopRemote = PlatformHooks.ObserveRemoteNotifications(func(n notification.LiveMessage, opened bool) {
			cx.Post(func() {
				if opened {
					m.openNotification(n.Data)
				} else {
					m.showLiveNotice(n)
				}
				m.change()
			})
		})
	}
	// Activity tokens belong to ActivityKit, never to the alert-push registration.
	// The desktop protocol has no activity subscription operation yet. A host
	// may forward these to an apns.ActivityManager through its own authenticated API.
	widget.OnPushToken(func(t widget.PushToken) {
		cx.Post(func() {
			if t.Activity != nil && m.push.activity != nil && t.Activity.ID() == m.push.activity.ID() && PlatformHooks.RegisterActivity != nil {
				PlatformHooks.RegisterActivity(apns.ActivityRegistration{ID: t.Activity.ID(), DeviceToken: t.Token, ResourceID: m.push.activityOwner.URL()})
			}
		})
	})
	permissionChecked := false
	cx.Effect(func() {
		m.revision.Get()
		if !permissionChecked && m.Loaded && cx.Env().Lifecycle() != core.LifecycleBackground {
			permissionChecked = true
			cx.Post(func() { m.requestPush(false) })
		}
		// Connection identity changes also for a retry within the same generation.
		owner := m.Link
		if !m.Connected {
			owner = ""
		}
		if m.push.owner != owner || m.push.conn != m.Conn || m.push.generation != m.Generation {
			m.push.owner, m.push.conn, m.push.generation, m.push.sent = owner, m.Conn, m.Generation, ""
		}
		m.syncPush()
		badge := len(m.State.Notifications.Unread)
		if m.push.disabled {
			badge = 0
		}
		cx.SetBadge(badge)
		m.openPendingNotification()
		m.openPendingActivity()
		m.syncActivity()
		if m.push.activity != nil {
			cx.AfterFunc(30*time.Second, m.change)
		}
	})
}

func (m *App) requestPush(ask bool) {
	if m.push.closed || m.push.disabled || m.push.requesting || !m.Loaded || m.cx.Env().Lifecycle() == core.LifecycleBackground {
		return
	}
	m.push.requesting = true
	answer := func(p core.NotificationPermission) {
		m.push.requesting = false
		if m.push.disabled || m.push.closed {
			return
		}
		m.push.granted = p == core.NotificationsGranted
		if !m.push.granted {
			m.push.err = tr("Allow Miya notifications in Settings")
			m.change()
			return
		}
		m.push.err = ""
		if m.cx.Env().OS() == core.OSIOS {
			if PlatformHooks.RegisterPush == nil {
				m.push.err = tr("Remote notifications are unavailable in this app build.")
			} else {
				m.registerPush()
			}
		}
		m.syncPush()
		m.change()
	}
	if ask {
		m.cx.RequestNotificationPermission(answer)
	} else {
		m.cx.NotificationPermission(answer)
	}
}
func (m *App) registerPush() {
	if m.push.requesting {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	m.push.cancel, m.push.requesting = cancel, true
	m.push.requestSeq++
	seq := m.push.requestSeq
	register := PlatformHooks.RegisterPush
	go func() {
		token, err := register(ctx)
		m.cx.Post(func() {
			if m.push.closed || seq != m.push.requestSeq {
				cancel()
				return
			}
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			cancel()
			m.push.cancel, m.push.requesting = nil, false
			if err != nil || token == "" {
				m.push.err = tr("Unable to register for notifications. Check system notification settings.")
			} else {
				m.push.token, m.push.err = token, ""
			}
			m.syncPush()
			m.change()
		})
	}()
}
func (m *App) syncPush() {
	live := m.cx.Env().OS() == core.OSAndroid && m.push.granted
	wantLease := live && !m.push.disabled && m.Link != ""
	if wantLease && m.push.keepRunning == nil && m.cx.Env().Lifecycle() != core.LifecycleBackground && PlatformHooks.KeepRunning != nil {
		m.push.keepRunning = PlatformHooks.KeepRunning(tr("Receiving notifications from your Mac"))
	}
	if !wantLease && m.push.keepRunning != nil {
		m.push.keepRunning()
		m.push.keepRunning = nil
	}
	m.KeepConnectionInBackground = m.push.keepRunning != nil
	if !m.Connected || !m.CanSend() || m.push.id == "" || (!m.push.disabled && (!m.push.granted || m.push.token == "" && !live)) {
		return
	}
	b, _ := json.Marshal(notification.Registration{ID: m.push.id, Token: m.push.token, Disabled: m.push.disabled, Desktop: m.desktopID(), Live: live})
	if string(b) == m.push.sent {
		return
	}
	m.push.sent = string(b)
	m.Action(remote.Request{Op: "push", Push: b})
}
func (m *App) shouldPresent(data map[string]string) bool {
	if m.push.disabled {
		return false
	}
	if d := data["desktop"]; d != "" && d != m.desktopID() {
		return true
	}
	if m.cx.Env().Lifecycle() == core.LifecycleBackground {
		return true
	}
	r, t := notification.Target(data), m.Viewer.Current()
	if r.ServerID != "" {
		return m.Open == 0 || t.ServerID != r.ServerID || r.Agent != "" && model.ThreadAgent(t) != r.Agent || r.Project != "" && m.ComposerProject() != r.Project
	}
	return m.Open == 0 || r.Thread != m.Open
}
func (m *App) showLiveNotice(n notification.LiveMessage) {
	if m.push.disabled {
		return
	}
	if n.Silent {
		if n.Badge != nil && *n.Badge == 0 {
			for key, id := range m.push.shown {
				m.cx.CloseNotification(id)
				delete(m.push.shown, key)
			}
		}
		return // authoritative read/state frames own badges; delayed pushes cannot restore them.
	}
	if n.ID == "" || n.Title == "" || !m.shouldPresent(n.Data) {
		return
	}
	id := notification.ID(n.ID, n.Data)
	if old := m.push.shown[n.ID]; old != "" {
		m.cx.CloseNotification(old)
	}
	_ = m.cx.ShowNotification(core.Notification{ID: id, Title: tr(n.Title), Body: n.Body}, nil)
	m.push.shown[n.ID] = id
}
func (m *App) openNotification(data map[string]string) {
	if d := data["desktop"]; m.Link != "" && d != "" && d != m.desktopID() {
		m.SendErr = tr("Select the desktop for this notification")
		return
	}
	m.push.pending, m.push.pendingDesktop = data, m.Link
	m.openPendingNotification()
}
func (m *App) openPendingNotification() {
	if m.push.pending == nil {
		return
	}
	if m.Link == "" {
		return
	}
	if d := m.push.pending["desktop"]; d != "" && d != m.desktopID() {
		m.SendErr = tr("Select the desktop for this notification")
		m.push.pending = nil
		return
	}
	if m.push.pendingDesktop != "" && m.push.pendingDesktop != m.Link {
		m.push.pending = nil
		return
	}
	r := notification.Target(m.push.pending)
	for _, p := range m.State.Projects {
		for _, t := range p.Threads {
			match := r.Thread != 0 && r.Thread == t.ID
			if r.ServerID != "" {
				match = r.ServerID == t.ServerID && (r.Agent == "" || r.Agent == model.ThreadAgent(t)) && (r.Project == "" || r.Project == p.Path)
			}
			if match {
				m.push.pending = nil
				m.cancelVoice()
				m.closeWebServices()
				m.closeFileBrowser()
				m.sheet.Set("")
				m.OpenThread(t)
				m.change()
				return
			}
		}
	}
}
func (m *App) openActivityURL(raw string) {
	ref, ok := model.ParseThreadURL(raw)
	if !ok {
		return
	}
	m.push.pendingActivity, m.push.activityDesktop = ref, m.Link
	m.openPendingActivity()
}
func (m *App) openPendingActivity() {
	ref := m.push.pendingActivity
	if ref.SessionID == "" || m.Link == "" || m.State.ReferenceSource == "" {
		return
	}
	if ref.Source != m.State.ReferenceSource || m.push.activityDesktop != "" && m.push.activityDesktop != m.Link {
		m.push.pendingActivity = model.ThreadRef{}
		m.SendErr = tr("Select the desktop for this notification")
		return
	}
	if t, p, found := m.ReferenceThread(ref); found {
		m.push.pendingActivity = model.ThreadRef{}
		m.openNotification(map[string]string{"serverId": t.ServerID, "agent": model.ThreadAgent(t), "project": p.Path})
	}
}
func (m *App) syncActivity() {
	t := m.Viewer.Current()
	ref := model.ThreadRef{Source: m.State.ReferenceSource, Backend: model.ThreadAgent(t), SessionID: t.ServerID}
	want := m.push.activities && !m.push.disabled && m.Connected && m.Path == "/thread" && t.Busy && ref.SessionID != "" && m.cx.Env().Lifecycle() != core.LifecycleBackground
	if m.push.activity != nil && (!want || ref != m.push.activityOwner) {
		m.endActivity()
	}
	if !want {
		return
	}
	content := widget.ActivityContent{Title: t.Title, Body: tr("Running"), Symbol: "bubble.left.and.bubble.right", URL: ref.URL(), Stale: time.Now().Add(time.Minute)}
	if content.Title == "" {
		content.Title = "Miya"
	}
	state := content.Title + content.Body
	if m.push.activity != nil {
		if state != m.push.activityState || time.Since(m.push.activityUpdated) >= 30*time.Second {
			if err := m.push.activity.Update(content); err == nil {
				m.push.activityState, m.push.activityUpdated = state, time.Now()
			}
		}
		return
	}
	if !widget.ActivitiesEnabled() {
		return
	}
	activity, err := widget.StartActivity("miya-conversation", content, widget.ActivityOptions{Push: PlatformHooks.RegisterActivity != nil})
	if err == nil {
		m.push.activity, m.push.activityOwner, m.push.activityState = activity, ref, state
		m.push.activityUpdated = time.Now()
	}
}
func (m *App) endActivity() {
	if a := m.push.activity; a != nil {
		_ = a.End(nil, widget.DismissNow)
		if PlatformHooks.RegisterActivity != nil {
			PlatformHooks.RegisterActivity(apns.ActivityRegistration{ID: a.ID(), ResourceID: m.push.activityOwner.URL()})
		}
		m.push.activity = nil
	}
}
func (m *App) closePush() {
	m.push.closed = true
	m.push.requestSeq++
	if m.push.cancel != nil {
		m.push.cancel()
	}
	if m.push.stopRemote != nil {
		m.push.stopRemote()
	}
	if m.push.keepRunning != nil {
		m.push.keepRunning()
	}
	m.endActivity()
	widget.OnPushToken(func(widget.PushToken) {})
	m.OnLiveNotification, m.OnPushResult = nil, nil
}
func (m *App) pushSettings(cx *core.Scope) core.Node {
	return scroll(text(tr("Notifications")),
		m.button("push-authorize", tr("Enable mobile notifications"), nil, func() { m.push.disabled = false; prefs.Save("phone-notifications", "on"); m.requestPush(true) }),
		m.button("push-disable", tr("Disable notifications"), nil, func() {
			m.push.disabled = true
			m.push.requestSeq++
			if m.push.cancel != nil {
				m.push.cancel()
				m.push.cancel = nil
			}
			m.push.requesting = false
			prefs.Save("phone-notifications", "off")
			m.syncPush()
			m.endActivity()
			for _, id := range m.push.shown {
				cx.CloseNotification(id)
			}
		}),
		m.button("live-activities", tr("Live Activities"), nil, func() {
			m.push.activities = !m.push.activities
			v := "off"
			if m.push.activities {
				v = "on"
			}
			prefs.Save("nsgo-phone-live-activities", v)
		}),
		core.Text{TestID: "push-status", ContentFunc: live(m, func() string {
			if m.push.disabled {
				return tr("Notifications disabled")
			}
			return m.push.err
		})},
		core.Text{ContentFunc: live(m, func() string {
			if m.push.activities {
				if !widget.ActivitiesEnabled() {
					return tr("Live Activities are unavailable in this app build.")
				}
				return tr("Live Activities enabled")
			}
			return tr("Live Activities disabled")
		})},
		m.button("push-done", tr("Done"), nil, func() { m.sheet.Set("") }))
}
