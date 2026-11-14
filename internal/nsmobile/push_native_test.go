package nsmobile

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	nativemobile "github.com/nsgo-dev/nsgo/mobile"
	"github.com/nsgo-dev/nsgo/widget"
	"miya/internal/apns"
	"miya/internal/chat/model"
	"miya/internal/notification"
	phoneclient "miya/internal/phone/client"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/prefs"
	"miya/internal/remote"
)

func TestNativePushRegistrationRotationAndPresentation(t *testing.T) {
	var emit func(nativemobile.NotificationEvent)
	calls := 0
	app, m, sent := startConfiguredPhone(t, core.OSIOS, func(m *App) {
		m.push.services.observe = func(fn func(nativemobile.NotificationEvent)) { emit = fn }
		m.push.services.register = func(p nativemobile.NotificationPresentation) error {
			calls++
			if p != 0 {
				t.Fatal("native banners bypass conversation suppression", p)
			}
			return nil
		}
	})
	app.Press("id:settings")
	app.Press("id:push-settings")
	app.Press("id:push-authorize")
	if calls != 1 || !m.push.requesting {
		t.Fatal("native registration not requested")
	}
	for _, token := range []string{strings.Repeat("a", 64), strings.Repeat("b", 64)} {
		emit(nativemobile.NotificationEvent{Kind: nativemobile.NotificationToken, Token: token})
		r := awaitRequest(t, app, sent, "push")
		var reg notification.Registration
		if json.Unmarshal(r.Push, &reg) != nil || reg.Token != token || reg.Live || len(reg.ID) != 32 {
			t.Fatal("bad APNs registration", string(r.Push))
		}
	}
	app.Press("id:push-done")
	openPhoneThread(t, app, m, sent)
	payload, _ := json.Marshal(apns.Payload{ID: "notice", Title: "Done", Body: "Result", Data: map[string]string{"serverId": "session", "agent": "codex", "project": "/work"}})
	emit(nativemobile.NotificationEvent{Kind: nativemobile.NotificationReceived, Remote: true, Payload: payload})
	app.Settle()
	if len(app.Notifications) != 0 {
		t.Fatal("open conversation presented")
	}
	app.Press("id:back")
	emit(nativemobile.NotificationEvent{Kind: nativemobile.NotificationReceived, Remote: true, Payload: payload})
	app.Settle()
	if len(app.Notifications) != 1 {
		t.Fatal("remote foreground notice missing")
	}
	// Do not treat a delayed silent APNs badge as authoritative unread state.
	zero := 0
	silent, _ := json.Marshal(apns.Payload{Badge: &zero, Silent: true})
	changePhone(app, m, func() {
		m.ApplyNotifications(notification.PhoneState{Revision: 4, Unread: []notification.Unread{{Key: "new", Version: 4}}})
	})
	emit(nativemobile.NotificationEvent{Kind: nativemobile.NotificationReceived, Remote: true, Payload: silent})
	app.Settle()
	if app.Badge != 1 || len(app.Notifications) != 0 {
		t.Fatal("silent receipt corrupted badges or failed to close notices")
	}
	emit(nativemobile.NotificationEvent{Kind: nativemobile.NotificationOpened, Remote: true, Payload: payload})
	app.Settle()
	if m.Open != 7 {
		t.Fatal("native open did not route", m.Open)
	}
	emit(nativemobile.NotificationEvent{Kind: nativemobile.NotificationRegistrationError, Err: errors.New("denied")})
	app.Settle()
	if m.push.requesting || m.push.err == "" {
		t.Fatal("native registration failure hidden")
	}
	old := emit
	changePhone(app, m, m.closePush)
	old(nativemobile.NotificationEvent{Kind: nativemobile.NotificationToken, Token: "late"})
	app.Settle()
	if m.push.token == "late" || emit != nil {
		t.Fatal("closed observer mutated app")
	}
}

func TestNativePushColdOpenWaitsForLoadAndSelectsDesktop(t *testing.T) {
	first, second := nativeLink(), nativeLink()
	var target string
	var emit func(nativemobile.NotificationEvent)
	app, m, _ := startConfiguredPhone(t, core.OSIOS, func(m *App) {
		m.Link = first
		d, _ := m.Desktops.Remember(first, "One")
		_ = d
		d, _ = m.Desktops.Remember(second, "Two")
		target = d.ID
		payload, _ := json.Marshal(apns.Payload{ID: "cold", Data: map[string]string{"desktop": target, "serverId": "cold-session", "agent": "codex", "project": "/two"}})
		m.push.services.observe = func(fn func(nativemobile.NotificationEvent)) {
			emit = fn
			if fn != nil {
				m.Loaded = false
				fn(nativemobile.NotificationEvent{Kind: nativemobile.NotificationOpened, Remote: true, Payload: payload})
			}
		}
		m.DialDesktop = func(ctx context.Context, _ string) (*remote.Conn, error) { <-ctx.Done(); return nil, ctx.Err() }
	})
	if m.Link != first || m.push.pending == nil {
		t.Fatal("cold open lost before load")
	}
	changePhone(app, m, func() { m.Loaded = true })
	if m.Link != second || m.push.pending == nil || m.Open != 0 {
		t.Fatal("wrong desktop or stale state routed", m.Link, m.Open)
	}
	changePhone(app, m, func() {
		m.Connected = true
		m.State.Projects = []model.ProjectSnapshot{{Path: "/two", Threads: []model.Thread{{ID: 88, ServerID: "cold-session", Agent: "codex", Title: "Cold conversation"}}}}
	})
	if m.Open != 88 || m.push.pending != nil {
		t.Fatal("cold open did not route after state", m.Open)
	}
	// Local native events must not open a second time through the remote path.
	emit(nativemobile.NotificationEvent{Kind: nativemobile.NotificationOpened, Remote: false, Payload: json.RawMessage(`{"data":{"thread":"7"}}`)})
	app.Settle()
	if m.Open != 88 {
		t.Fatal("local open delivered twice")
	}
	m.Close()
	app.Settle()
}

type testPushActivity struct {
	id            string
	updates, ends int
}

func (a *testPushActivity) ID() string                           { return a.id }
func (a *testPushActivity) UpdateJSON(raw json.RawMessage) error { a.updates++; return nil }
func (a *testPushActivity) EndJSON(raw json.RawMessage, d widget.Dismissal) error {
	a.ends++
	return nil
}

func TestNativeActivityTokensRotateReconnectAndUnregister(t *testing.T) {
	a := &testPushActivity{id: "activity-one"}
	var token func(string, string)
	app, m, sent := startConfiguredPhone(t, core.OSIOS, func(m *App) {
		m.push.services.observeActivity = func(fn func(string, string)) { token = fn }
		m.push.services.activitiesEnabled = func() bool { return true }
		m.push.services.startActivity = func(kind string, raw json.RawMessage, opts ...widget.ActivityOptions) (phoneActivity, error) {
			if kind != "miya-conversation" || len(opts) != 1 || !opts[0].Push || !json.Valid(raw) {
				t.Fatal("wrong widget request")
			}
			return a, nil
		}
	})
	openPhoneThread(t, app, m, sent)
	changePhone(app, m, func() { m.push.activities = true; th := m.Viewer.Current(); th.Busy = true; m.ShowThread(th) })
	for _, v := range []string{strings.Repeat("c", 64), strings.Repeat("d", 64)} {
		token(a.id, v)
		r := awaitRequest(t, app, sent, "activity")
		var reg phoneproto.ActivityRegistration
		if json.Unmarshal(r.Activity, &reg) != nil || reg.ID != a.id || reg.Token != v || reg.Reference != m.push.activityOwner.URL() || reg.DeviceID != m.push.id {
			t.Fatal("bad activity registration", string(r.Activity))
		}
	}
	changePhone(app, m, func() { m.Generation++ })
	r := awaitRequest(t, app, sent, "activity")
	var reg phoneproto.ActivityRegistration
	_ = json.Unmarshal(r.Activity, &reg)
	if reg.Token != strings.Repeat("d", 64) {
		t.Fatal("reconnect lost activity token")
	}
	// Background retains the activity and its desktop subscription.
	app.SetEnvironment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone, Lifecycle: core.LifecycleBackground})
	if a.ends != 0 || m.push.activity == nil {
		t.Fatal("background ended push activity")
	}
	changePhone(app, m, func() { m.Connected = true; m.Path = "/" })
	r = awaitRequest(t, app, sent, "activity")
	reg = phoneproto.ActivityRegistration{}
	_ = json.Unmarshal(r.Activity, &reg)
	if reg.Token != "" || a.ends != 1 {
		t.Fatal("navigation did not unregister/end", string(r.Activity), a.ends)
	}
}

func TestNativeForegroundServiceLeasesAndLateCompletions(t *testing.T) {
	var starts []nativemobile.ForegroundService
	var completions []func(error)
	stops := 0
	app, m, sent := startConfiguredPhone(t, core.OSAndroid, func(m *App) {
		m.push.services.startForeground = func(n nativemobile.ForegroundService, done func(error)) error {
			starts = append(starts, n)
			completions = append(completions, done)
			return nil
		}
		m.push.services.stopForeground = func(done func(error)) error {
			stops++
			if done != nil {
				done(nil)
			}
			return nil
		}
	})
	openPhoneThread(t, app, m, sent)
	changePhone(app, m, func() { m.push.disabled = true; m.State.Projects[0].Threads[0].Busy = true })
	if len(starts) != 1 || starts[0].Types != nativemobile.ForegroundDataSync || m.KeepConnectionInBackground {
		t.Fatal("unconfirmed service retained connection")
	}
	completions[0](nil)
	app.Settle()
	if !m.KeepConnectionInBackground {
		t.Fatal("promoted service did not acquire lease")
	}
	changePhone(app, m, func() {
		m.voice = &phoneVoice{session: &testVoice{}, cancel: func() {}, owner: m.composerOwner()}
		m.DesktopPage = &phoneclient.MobileDesktopPage{Active: true, Phase: "live"}
		m.State.Projects[0].Threads[0].Busy = false
	})
	if len(starts) != 2 || starts[1].Types != nativemobile.ForegroundDataSync|nativemobile.ForegroundMicrophone {
		t.Fatal("operation types not combined", starts)
	}
	completions[1](nil)
	app.Settle()
	changePhone(app, m, func() { m.voice = nil })
	if len(starts) != 3 || starts[2].Types != nativemobile.ForegroundDataSync || stops != 0 {
		t.Fatal("recording release stopped desktop lease")
	}
	completions[2](nil)
	app.Settle()
	changePhone(app, m, func() { m.DesktopPage = nil })
	if stops != 1 || m.KeepConnectionInBackground {
		t.Fatal("last lease not released", stops)
	}
	changePhone(app, m, func() { m.State.Projects[0].Threads[0].Busy = true })
	completions[3](errors.New("promotion denied"))
	app.Settle()
	if m.KeepConnectionInBackground || len(starts) != 4 {
		t.Fatal("failed promotion acquired/retried lease")
	}
	changePhone(app, m, func() { m.State.Projects[0].Threads[0].Busy = false })
	changePhone(app, m, func() { m.State.Projects[0].Threads[0].Busy = true })
	late := completions[4]
	changePhone(app, m, m.closePush)
	count := stops
	late(nil)
	app.Settle()
	if m.KeepConnectionInBackground || stops != count {
		t.Fatal("late service callback resurrected lease")
	}
	// No real remote desktop client was created by this fixture.
	m.DesktopPage = nil
}

func TestNativeActivityRelaunchAdoptsNativeActivityAndToken(t *testing.T) {
	a := &testPushActivity{id: "restored"}
	ref := model.ThreadRef{Source: "0123456789abcdef0123456789abcdef", Backend: "codex", SessionID: "session"}
	app, m, sent := startConfiguredPhone(t, core.OSIOS, func(m *App) {
		raw, _ := json.Marshal(savedPushActivity{ID: a.id, Reference: ref.URL()})
		prefs.Save("nsgo-phone-activity", string(raw))
		prefs.Save("nsgo-phone-live-activities", "on")
		th := m.State.Projects[0].Threads[0]
		th.Busy = true
		m.Viewer.SetThreads([]model.Thread{th})
		m.Viewer.SetActive(th.ID)
		m.Open = th.ID
		m.Path = "/thread"
		m.push.services.listActivities = func() []phoneActivity { return []phoneActivity{a} }
		m.push.services.observeActivity = func(fn func(string, string)) {
			if fn != nil {
				fn(a.id, strings.Repeat("f", 64))
			}
		}
		m.push.services.activitiesEnabled = func() bool { return true }
		m.push.services.startActivity = func(string, json.RawMessage, ...widget.ActivityOptions) (phoneActivity, error) {
			t.Fatal("relaunch created duplicate activity")
			return nil, nil
		}
	})
	r := awaitRequest(t, app, sent, "activity")
	var reg phoneproto.ActivityRegistration
	_ = json.Unmarshal(r.Activity, &reg)
	if m.push.activity != a || reg.ID != a.id || reg.Token != strings.Repeat("f", 64) || a.ends != 0 {
		t.Fatal("existing activity/token not restored", string(r.Activity))
	}
}

func TestNativeActivityURLSelectsDesktopBeforeResolvingThread(t *testing.T) {
	first, second := nativeLink(), nativeLink()
	app, m, _ := startConfiguredPhone(t, core.OSIOS, func(m *App) {
		m.Link = first
		_, _ = m.Desktops.Remember(first, "First")
		_, _ = m.Desktops.Remember(second, "Second")
		m.DialDesktop = func(ctx context.Context, _ string) (*remote.Conn, error) { <-ctx.Done(); return nil, ctx.Err() }
	})
	target, _ := m.Desktops.Remember(second, "")
	ref := model.ThreadRef{Source: strings.Repeat("d", 32), Backend: "codex", SessionID: "other"}
	changePhone(app, m, func() { m.openActivityURL(phoneproto.ActivityURL(ref.URL(), target.ID)) })
	if m.Link != second || m.push.pendingActivity != ref {
		t.Fatal("activity did not select target desktop")
	}
	changePhone(app, m, func() {
		m.Connected = true
		m.State.ReferenceSource = ref.Source
		m.State.Projects = []model.ProjectSnapshot{{Path: "/other", Threads: []model.Thread{{ID: 90, Agent: "codex", ServerID: "other"}}}}
	})
	if m.Open != 90 || m.push.pendingActivity.SessionID != "" {
		t.Fatal("activity did not resolve durable conversation", m.Open)
	}
	m.Close()
	app.Settle()
}

func TestNativeForegroundLastLeaseDisconnectsInBackground(t *testing.T) {
	var promote func(error)
	stops := 0
	app, m, _ := startConfiguredPhone(t, core.OSAndroid, func(m *App) {
		m.push.services.startForeground = func(_ nativemobile.ForegroundService, done func(error)) error { promote = done; return nil }
		m.push.services.stopForeground = func(done func(error)) error {
			stops++
			if done != nil {
				done(nil)
			}
			return nil
		}
	})
	changePhone(app, m, func() { m.push.disabled = true; m.State.Projects[0].Threads[0].Busy = true })
	promote(nil)
	app.Settle()
	app.SetEnvironment(core.Environment{OS: core.OSAndroid, Idiom: core.IdiomPhone, Lifecycle: core.LifecycleBackground})
	if !m.Connected || !m.KeepConnectionInBackground {
		t.Fatal("acknowledged lease did not retain connection")
	}
	changePhone(app, m, func() { m.State.Projects[0].Threads[0].Busy = false })
	if stops != 1 || m.Connected || m.KeepConnectionInBackground {
		t.Fatal("last background lease did not stop and disconnect")
	}
}

func TestNativeAndroidLiveRegistrationDoesNotSubmitFCMToken(t *testing.T) {
	var emit func(nativemobile.NotificationEvent)
	app, m, sent := startConfiguredPhone(t, core.OSAndroid, func(m *App) {
		m.push.services.observe = func(fn func(nativemobile.NotificationEvent)) { emit = fn }
		m.push.services.register = func(nativemobile.NotificationPresentation) error {
			t.Fatal("paired Android delivery requested APNs registration")
			return nil
		}
		m.push.services.startForeground = func(_ nativemobile.ForegroundService, done func(error)) error { done(nil); return nil }
		m.push.services.stopForeground = func(done func(error)) error {
			if done != nil {
				done(nil)
			}
			return nil
		}
	})
	app.Press("id:settings")
	app.Press("id:push-settings")
	app.Press("id:push-authorize")
	r := awaitRequest(t, app, sent, "push")
	var reg notification.Registration
	_ = json.Unmarshal(r.Push, &reg)
	emit(nativemobile.NotificationEvent{Kind: nativemobile.NotificationToken, Token: "opaque-fcm-token"})
	app.Settle()
	if !reg.Live || reg.Token != "" || m.push.token != "" || !m.KeepConnectionInBackground {
		t.Fatal("Android delivery mixed with APNs", string(r.Push))
	}
}

func TestNativeBackgroundAPNsAlertDoesNotDuplicateSystemPresentation(t *testing.T) {
	var emit func(nativemobile.NotificationEvent)
	app, _, _ := startConfiguredPhone(t, core.OSIOS, func(m *App) { m.push.services.observe = func(fn func(nativemobile.NotificationEvent)) { emit = fn } })
	app.SetEnvironment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone, Lifecycle: core.LifecycleBackground})
	payload, _ := json.Marshal(apns.Payload{ID: "system-alert", Title: "Done", Data: map[string]string{"thread": "7"}})
	emit(nativemobile.NotificationEvent{Kind: nativemobile.NotificationReceived, Remote: true, Payload: payload})
	app.Settle()
	if len(app.Notifications) != 0 {
		t.Fatal("background APNs alert duplicated locally")
	}
}
