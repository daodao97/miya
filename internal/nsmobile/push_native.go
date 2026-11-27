package nsmobile

import (
	"encoding/json"
	"errors"

	"github.com/nsgo-dev/nsgo/core"
	nativemobile "github.com/nsgo-dev/nsgo/mobile"
	"github.com/nsgo-dev/nsgo/widget"
	"miya/internal/apns"
	"miya/internal/chat/model"
	"miya/internal/notification"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/prefs"
	"miya/internal/remote"
)

// Per-app seams let nsgotest exercise asynchronous native replies without an
// APNs connection, Android service or packaged ActivityKit extension.
type phoneActivity interface {
	ID() string
	Update(widget.ActivityContent) error
	End(*widget.ActivityContent, widget.Dismissal) error
}

type phoneServices struct {
	observe           func(func(mobileNotificationEvent))
	register          func(int) error
	startForeground   func(mobileForegroundService, func(error)) error
	stopForeground    func(func(error)) error
	activitiesEnabled func() bool
	listActivities    func() []phoneActivity
	startActivity     func(string, widget.ActivityContent, ...widget.ActivityOptions) (phoneActivity, error)
	observeActivity   func(func(string, string))
}

type mobileNotificationEvent struct {
	Kind    int
	Token   string
	Remote  bool
	Payload json.RawMessage
}

const (
	mobileNotificationToken = iota
	mobileNotificationRegistrationError
	mobileNotificationReceived
	mobileNotificationOpened
)

type mobileForegroundServiceType uint8

const (
	mobileForegroundDataSync mobileForegroundServiceType = 1 << iota
	mobileForegroundMicrophone
)

type mobileForegroundService struct {
	ID, Title, Body string
	Types           mobileForegroundServiceType
}

var errNativePushUnavailable = errors.New("this NSGo host does not provide APNs registration")

func (s *phoneServices) defaults() {
	if s.observe == nil {
		s.observe = func(func(mobileNotificationEvent)) {}
	}
	if s.register == nil {
		s.register = func(int) error { return errNativePushUnavailable }
	}
	if s.startForeground == nil || s.stopForeground == nil {
		var lease *nativemobile.DataSync
		if s.startForeground == nil {
			s.startForeground = func(service mobileForegroundService, done func(error)) error {
				if service.Types&mobileForegroundDataSync != 0 {
					next, err := nativemobile.StartDataSync(nativemobile.DataSyncOptions{
						Notification: nativemobile.DataSyncNotification{Title: service.Title, Body: service.Body},
					})
					if err != nil {
						return err
					}
					lease.Stop()
					lease = next
				} else {
					lease.Stop()
					lease = nil
				}
				if done != nil {
					done(nil)
				}
				return nil
			}
		}
		if s.stopForeground == nil {
			s.stopForeground = func(done func(error)) error {
				lease.Stop()
				lease = nil
				if done != nil {
					done(nil)
				}
				return nil
			}
		}
	}
	if s.activitiesEnabled == nil {
		s.activitiesEnabled = widget.ActivitiesEnabled
	}
	if s.listActivities == nil {
		s.listActivities = func() []phoneActivity {
			var out []phoneActivity
			for _, a := range widget.Activities() {
				out = append(out, a)
			}
			return out
		}
	}
	if s.startActivity == nil {
		s.startActivity = func(kind string, content widget.ActivityContent, opts ...widget.ActivityOptions) (phoneActivity, error) {
			a, err := widget.StartActivity(kind, content, opts...)
			if err != nil {
				return nil, err
			}
			return a, nil
		}
	}
	if s.observeActivity == nil {
		s.observeActivity = func(fn func(string, string)) {
			if fn == nil {
				widget.OnPushToken(func(widget.PushToken) {})
				return
			}
			widget.OnPushToken(func(t widget.PushToken) {
				if t.Activity != nil {
					fn(t.Activity.ID(), t.Token)
				}
			})
		}
	}
}

func (m *App) notificationEvent(n mobileNotificationEvent) {
	switch n.Kind {
	case mobileNotificationToken:
		// Android uses paired live delivery, not the desktop's APNs provider. An
		// embedding host's FCM token must never be submitted as an APNs token.
		if m.cx.Env().OS() != core.OSIOS || n.Token == "" {
			return
		}
		m.push.token, m.push.err, m.push.requesting = n.Token, "", false
		m.syncPush()
	case mobileNotificationRegistrationError:
		m.push.requesting = false
		m.push.err = tr("Unable to register for notifications. Check system notification settings.")
	case mobileNotificationReceived, mobileNotificationOpened:
		if !n.Remote {
			return
		} // local presses have their own scope callback
		notice, ok := decodeRemoteNotice(n.Payload)
		if !ok {
			return
		}
		if n.Kind == mobileNotificationOpened {
			m.openNotification(notice.Data)
		} else if notice.Silent || m.cx.Env().OS() != core.OSIOS || m.cx.Env().Lifecycle() != core.LifecycleBackground {
			// iOS already presents background APNs alerts. Only foreground receipts
			// need the adapter's local presentation and conversation suppression.
			m.showLiveNotice(notice)
		}
	}
}

// APNs is internal/apns.Payload's wire shape, not LiveMessage's Go shape.
// Android embedding hosts can forward the same object or a paired notice.
func decodeRemoteNotice(raw json.RawMessage) (notification.LiveMessage, bool) {
	var p struct {
		APS struct {
			Alert struct{ Title, Body string }
			Badge *int
			Sound string
		} `json:"aps"`
		ID   string            `json:"mygoID"`
		Data map[string]string `json:"mygo"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return notification.LiveMessage{}, false
	}
	if p.Data != nil || p.ID != "" || p.APS.Badge != nil {
		return notification.LiveMessage{ID: p.ID, Title: p.APS.Alert.Title, Body: p.APS.Alert.Body, Badge: p.APS.Badge, Silent: p.APS.Alert.Title == "" && p.APS.Alert.Body == "", Data: p.Data}, true
	}
	var live notification.LiveMessage
	if json.Unmarshal(raw, &live) != nil || live.Data == nil {
		return live, false
	}
	return live, true
}

// Only public IDs are persisted. Tokens remain in memory and provider keys
// remain on the desktop. Adopt an existing native activity after a relaunch.
type savedPushActivity struct{ ID, Reference, Desktop string }

func (m *App) saveActivity() {
	r := savedPushActivity{m.push.activity.ID(), m.push.activityOwner.URL(), m.desktopID()}
	raw, _ := json.Marshal(r)
	prefs.Save("nsgo-phone-activity", string(raw))
}
func (m *App) restoreActivity() bool {
	r := m.push.restoredActivity
	if r == nil {
		return true
	}
	if !m.Loaded || m.Link != "" && (!m.Connected || m.State.ReferenceSource == "") {
		return false
	}
	ref, valid := model.ParseThreadURL(r.Reference)
	for _, a := range m.push.services.listActivities() {
		if a.ID() != r.ID {
			continue
		}
		if valid && r.Desktop == m.desktopID() && m.Link != "" && ref.Source == m.State.ReferenceSource {
			m.push.activity, m.push.activityOwner, m.push.activityLink = a, ref, m.Link
			m.push.activityToken = m.push.restoredToken
		} else {
			_ = a.End(nil, widget.DismissNow)
		}
		break
	}
	m.push.restoredActivity = nil
	m.push.restoredToken = ""
	if m.push.activity == nil {
		prefs.Save("nsgo-phone-activity", "")
	}
	return true
}

type pushRequest struct {
	request    remote.Request
	conn       *remote.Conn
	send       func(remote.Request) error
	generation int
}

// Token rotations, unregisters and alert registration must retain UI order.
// Client.Action starts independent workers and cannot provide that ordering.
func (m *App) sendPush(r remote.Request) {
	if !m.CanSend() {
		return
	}
	q := pushRequest{r, m.Conn, m.SendRequest, m.Generation}
	select {
	case m.push.sendQueue <- q:
	default:
		m.push.sent, m.push.activitySent = "", ""
		m.push.err = tr("Unable to register for notifications. Check system notification settings.")
	}
}
func (m *App) runPushQueue(queue <-chan pushRequest) {
	for q := range queue {
		var err error
		if q.send != nil {
			err = q.send(q.request)
		} else {
			err = q.conn.Send(q.request)
		}
		if err != nil {
			m.cx.Post(func() {
				if !m.push.closed && m.Conn == q.conn && m.Generation == q.generation {
					m.push.sent, m.push.activitySent = "", ""
					m.push.err = tr("Action failed: ") + err.Error()
					m.change()
				}
			})
		}
	}
}
func (m *App) registerActivity(r apns.ActivityRegistration) {
	if PlatformHooks.RegisterActivity != nil {
		PlatformHooks.RegisterActivity(r)
		return
	}
	// Never send an old desktop's subscription on a newly selected connection.
	if !m.Connected || m.Link != m.push.activityLink {
		return
	}
	b, _ := json.Marshal(phoneproto.ActivityRegistration{DeviceID: m.push.id, Desktop: m.desktopID(), ID: r.ID, Token: r.DeviceToken, Reference: r.ResourceID})
	m.sendPush(remote.Request{Op: "activity", Activity: b})
}
func (m *App) syncActivityToken() {
	if m.push.activity == nil || m.push.activityToken == "" || !m.Connected || m.Link != m.push.activityLink {
		return
	}
	key := m.push.activity.ID() + ":" + m.push.activityToken
	if key == m.push.activitySent {
		return
	}
	m.push.activitySent = key
	m.registerActivity(apns.ActivityRegistration{ID: m.push.activity.ID(), DeviceToken: m.push.activityToken, ResourceID: m.push.activityOwner.URL()})
}

type foregroundLease struct {
	desired, active, attempted mobileForegroundServiceType
	pending                    bool
	body, activeBody           string
}

func (m *App) syncForeground(live bool) {
	if m.cx.Env().OS() != core.OSAndroid {
		return
	}
	body := tr("Running")
	var want mobileForegroundServiceType
	if live && !m.push.disabled && m.Link != "" {
		want |= mobileForegroundDataSync
		body = tr("Receiving notifications from your Mac")
	}
	if m.Connected {
		if m.voice != nil && m.voice.session != nil && !m.voice.released {
			want |= mobileForegroundMicrophone
		}
		if p := m.DesktopPage; p != nil && p.Active && p.Phase != "ended" && p.Phase != "error" {
			want |= mobileForegroundDataSync
			body = tr("Remote desktop")
		}
		for _, p := range m.State.Projects {
			for _, t := range p.Threads {
				if t.Busy {
					want |= mobileForegroundDataSync
				}
			}
		}
		if m.Viewer.Current().Busy {
			want |= mobileForegroundDataSync
		}
	}
	if want&mobileForegroundMicrophone != 0 {
		body = tr("Listening… Release to finish")
	}
	f := &m.push.foreground
	if want != f.desired || body != f.body {
		f.attempted = 0
		f.desired, f.body = want, body
	}
	if PlatformHooks.KeepRunning != nil {
		if want != 0 && m.push.keepRunning == nil && m.cx.Env().Lifecycle() != core.LifecycleBackground {
			m.push.keepRunning = PlatformHooks.KeepRunning(tr("Receiving notifications from your Mac"))
		}
		if want == 0 && m.push.keepRunning != nil {
			m.push.keepRunning()
			m.push.keepRunning = nil
		}
		m.KeepConnectionInBackground = m.push.keepRunning != nil
		return
	}
	m.applyForeground()
}
func (m *App) applyForeground() {
	f := &m.push.foreground
	m.KeepConnectionInBackground = f.active != 0
	if !m.KeepConnectionInBackground && m.cx.Env().Lifecycle() == core.LifecycleBackground && m.Connected {
		m.Disconnect()
	}
	if m.push.closed || f.pending || f.desired == f.active && (f.desired == 0 || f.body == f.activeBody) {
		return
	}
	want, body := f.desired, f.body
	if want != 0 && (m.cx.Env().Lifecycle() == core.LifecycleBackground || f.attempted == want) {
		return
	}
	f.pending, f.attempted = true, want
	done := func(err error) {
		m.cx.Post(func() {
			if m.push.closed {
				return
			}
			f.pending = false
			if err == nil {
				f.active, f.activeBody = want, body
			} else {
				// An update can fail while an earlier service is still running. Stop it
				// rather than claiming a microphone/type lease Android did not grant.
				f.active = 0
				_ = m.push.services.stopForeground(nil)
				if !errors.Is(err, nativemobile.ErrDataSyncUnavailable) {
					m.push.err = tr("Action failed: ") + err.Error()
				}
			}
			m.applyForeground()
			m.change()
		})
	}
	var err error
	if want == 0 {
		err = m.push.services.stopForeground(done)
	} else {
		err = m.push.services.startForeground(mobileForegroundService{ID: "miya-running", Title: "Miya", Body: body, Types: want}, done)
	}
	if err != nil {
		done(err)
	}
}
func (m *App) closeForeground() {
	f := &m.push.foreground
	if f.pending || f.active != 0 {
		_ = m.push.services.stopForeground(nil)
	}
	f.active, f.desired = 0, 0
	m.KeepConnectionInBackground = false
}
