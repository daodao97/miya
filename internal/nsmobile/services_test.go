package nsmobile

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/microphone"
	"github.com/nsgo-dev/nsgo/nsgotest"
	"miya/internal/chat/model"
	"miya/internal/notification"
	"miya/internal/remote"
	rt "miya/internal/runtime"
	"miya/internal/runtime/tracker"
	webservice "miya/internal/runtime/web"
	"miya/internal/voiceinput"
)

type testVoice struct{ stopped, canceled atomic.Int32 }

func (v *testVoice) Stop()   { v.stopped.Add(1) }
func (v *testVoice) Cancel() { v.canceled.Add(1) }
func holdVoice(t *testing.T, app *nsgotest.App, m *App) *nsgotest.Finger {
	t.Helper()
	finger := app.Touch("id:voice-hold", core.Point{X: 10, Y: 10})
	app.Advance(400 * time.Millisecond)
	app.WaitUntil(time.Second, func() bool { return m.voice != nil && m.voice.session != nil })
	return finger
}

func TestPhoneVoiceHoldWorksFromComposerOnHome(t *testing.T) {
	for _, target := range []struct {
		selector string
		at       core.Point
	}{
		{"id:mobile-composer", core.Point{X: 150, Y: 26}},
		{"id:voice-hold", core.Point{X: 22, Y: 22}},
	} {
		t.Run(target.selector, func(t *testing.T) {
			v := &testVoice{}
			ready := make(chan func(voiceinput.Result), 1)
			app, m, sent := startConfiguredPhone(t, core.OSIOS, func(m *App) {
				m.voiceAutoSend = false
				m.voiceStart = func(_ context.Context, _ string, emit func(voiceinput.Result)) (voiceSession, error) {
					ready <- emit
					return v, nil
				}
			})
			if !app.Exists("id:mobile-home") || !app.Exists("label:"+tr("Hold to speak")) {
				t.Fatal("the empty composer does not show a voice control")
			}
			app.PressAt(target.selector, target.at)
			app.Advance(400 * time.Millisecond)
			if m.voice != nil {
				t.Fatal("a tap started recording")
			}
			if target.selector == "id:mobile-composer" {
				if !app.Find(target.selector).Focused {
					t.Fatal("tapping the empty composer did not open the editor")
				}
				finger := app.Touch(target.selector, target.at)
				finger.Hold(400 * time.Millisecond)
				finger.Lift()
				if !app.Find(target.selector).Focused || len(ready) != 0 || m.voice != nil {
					t.Fatal("holding the focused composer interrupted text editing")
				}
				changePhone(app, m, func() { m.cx.DismissKeyboard() })
			}
			app.LongPressAt(target.selector, target.at)
			app.WaitUntil(time.Second, func() bool { return v.stopped.Load() == 1 })
			if app.Find("id:mobile-composer").Focused {
				t.Fatal("holding to speak opened the editor")
			}
			emit := <-ready
			emit(voiceinput.Result{Text: "语音输入", Final: true})
			app.WaitUntil(time.Second, func() bool { return m.voice == nil && m.Draft == "语音输入" })
			if app.Find("id:mobile-composer").Text != "语音输入" || !app.Exists("label:"+tr("Send")) {
				t.Fatal("the composer did not retain the dictation and return to Send")
			}
			if app.Find("id:mobile-composer").Focused {
				t.Fatal("inserting the recognized text reopened the editor")
			}
			select {
			case r := <-sent:
				t.Fatalf("dictation sent a request with auto-send off: %s", r.Op)
			default:
			}
		})
	}
}

func TestPhoneVoiceInsertsPartialsAtSelectionAndFinalOnRelease(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	v := &testVoice{}
	ready := make(chan func(voiceinput.Result), 1)
	changePhone(app, m, func() {
		m.voiceStart = func(_ context.Context, _ string, emit func(voiceinput.Result)) (voiceSession, error) {
			ready <- emit
			return v, nil
		}
	})
	app.Type("id:mobile-composer", "Hi 😀 old tail")
	app.Select("id:mobile-composer", 6, 9) // UTF-16 selection replaces "old".
	finger := holdVoice(t, app, m)
	if app.Find("id:mobile-composer").Focused {
		t.Fatal("recording from the microphone button left the editor focused")
	}
	emit := <-ready
	emit(voiceinput.Result{Text: "par"})
	app.Settle()
	if m.Draft != "Hi 😀 par tail" || app.Find("id:mobile-composer").Text != m.Draft || app.Find("id:mobile-composer").Selection.End != 9 {
		t.Fatalf("partial not at caret: %q", m.Draft)
	}
	emit(voiceinput.Result{Text: " final ", Final: true})
	app.Settle()
	if m.voice == nil {
		t.Fatal("finished before release")
	}
	finger.Lift()
	if m.Draft != "Hi 😀 final tail" || m.voice != nil || m.Sending {
		t.Fatalf("final: draft=%q voice=%v sending=%v", m.Draft, m.voice, m.Sending)
	}
}
func TestPhoneVoiceAutoSendAndCancellation(t *testing.T) {
	for _, action := range []string{"send", "slide", "system", "navigation", "disconnect", "background", "epoch"} {
		t.Run(action, func(t *testing.T) {
			app, m, sent := startPhone(t)
			openPhoneThread(t, app, m, sent)
			v := &testVoice{}
			ready := make(chan func(voiceinput.Result), 1)
			changePhone(app, m, func() {
				m.voiceAutoSend = true
				m.Draft = "original "
				m.caret = len(m.Draft)
				m.anchor = m.caret
				m.voiceStart = func(_ context.Context, _ string, emit func(voiceinput.Result)) (voiceSession, error) {
					ready <- emit
					return v, nil
				}
			})
			finger := holdVoice(t, app, m)
			emit := <-ready
			emit(voiceinput.Result{Text: "partial"})
			app.Settle()
			switch action {
			case "send":
				finger.Lift()
				if v.stopped.Load() != 1 {
					t.Fatal("release did not stop capture")
				}
			case "slide":
				finger.Move(core.Point{X: -100, Y: 10})
				finger.Lift()
			case "system":
				finger.Cancel()
			case "navigation":
				goPhoneHome(app)
			case "disconnect":
				changePhone(app, m, m.Disconnect)
			case "background":
				app.SetEnvironment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone, Lifecycle: core.LifecycleBackground})
			case "epoch":
				changePhone(app, m, func() { m.StartConversation("/work", "codex"); m.Path = "/thread"; m.Draft = "new draft" })
			}
			emit(voiceinput.Result{Text: "spoken", Final: true})
			app.Settle()
			if action == "send" {
				r := awaitRequest(t, app, sent, "send")
				if r.Text != "original spoken" {
					t.Fatalf("auto-send %q", r.Text)
				}
			} else {
				if m.voice != nil || v.canceled.Load() == 0 || m.Sending || strings.Contains(m.Draft, "partial") || strings.Contains(m.Draft, "spoken") {
					t.Fatalf("cancel leaked text/send: %q", m.Draft)
				}
				if action == "epoch" && m.Draft != "new draft" {
					t.Fatal("new owner draft overwritten")
				}
			}
		})
	}
}
func TestPhoneVoiceReleaseDuringPermissionAndDenied(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	waiting, unblock := make(chan struct{}), make(chan struct{})
	v := &testVoice{}
	changePhone(app, m, func() {
		m.voiceStart = func(ctx context.Context, _ string, _ func(voiceinput.Result)) (voiceSession, error) {
			close(waiting)
			<-unblock
			return v, nil
		}
	})
	finger := app.Touch("id:voice-hold", core.Point{X: 10, Y: 10})
	app.Advance(400 * time.Millisecond)
	select {
	case <-waiting:
	case <-time.After(time.Second):
		t.Fatal("no start")
	}
	finger.Lift()
	close(unblock)
	app.WaitUntil(time.Second, func() bool { return v.canceled.Load() > 0 })
	if m.voice != nil {
		t.Fatal("late capture survived release")
	}
	changePhone(app, m, func() {
		m.voiceStart = func(context.Context, string, func(voiceinput.Result)) (voiceSession, error) {
			return nil, microphone.ErrDenied
		}
	})
	app.Touch("id:voice-hold", core.Point{X: 10, Y: 10})
	app.Advance(400 * time.Millisecond)
	app.WaitUntil(time.Second, func() bool { return m.SendErr == tr("Allow microphone access in system settings to use voice input.") })
}
func TestPhonePushRegistrationRoutingAndBadges(t *testing.T) {
	previous := PlatformHooks
	defer func() { PlatformHooks = previous }()
	var registrations atomic.Int32
	PlatformHooks.RegisterPush = func(context.Context) (string, error) { registrations.Add(1); return strings.Repeat("a", 64), nil }
	app, m, sent := startPhone(t)
	openPhoneDrawer(app)
	app.Press("id:settings")
	app.Press("id:push-settings")
	app.Press("id:push-authorize")
	r := awaitRequest(t, app, sent, "push")
	var registration notification.Registration
	if err := json.Unmarshal(r.Push, &registration); err != nil {
		t.Fatal(err)
	}
	if registration.Token != strings.Repeat("a", 64) || len(registration.ID) != 32 || registration.Live || registration.Disabled || registrations.Load() != 1 {
		t.Fatalf("registration %#v", registration)
	}
	changePhone(app, m, func() { m.pages.Open("") })
	openPhoneThread(t, app, m, sent)
	changePhone(app, m, func() {
		m.showLiveNotice(notification.LiveMessage{ID: "current", Title: "Done", Data: map[string]string{"thread": "999", "serverId": "session", "agent": "codex", "project": "/work"}})
		m.ApplyNotifications(notification.PhoneState{Revision: 2, Unread: []notification.Unread{{Key: "one", Thread: "1", Version: 1}, {Key: "two", Thread: "2", Version: 2}}})
	})
	if len(app.Notifications) != 0 || app.Badge != 2 {
		t.Fatalf("foreground/badge: %v %d", app.Notifications, app.Badge)
	}
	goPhoneHome(app)
	changePhone(app, m, func() {
		m.showLiveNotice(notification.LiveMessage{ID: "done", Title: "Done", Data: map[string]string{"thread": "999", "serverId": "session", "agent": "codex", "project": "/work"}})
	})
	if len(app.Notifications) != 1 {
		t.Fatal("notice not shown")
	}
	app.PressNotification(app.Notifications[0].ID)
	if m.Open != 7 {
		t.Fatal("notification did not use durable identity", m.Open)
	}
	changePhone(app, m, func() { m.openNotification(map[string]string{"desktop": "other", "thread": "7"}) })
	if m.SendErr != tr("Select the desktop for this notification") {
		t.Fatal("foreign desktop was not rejected")
	}
	goPhoneHome(app)
	openPhoneDrawer(app)
	app.Press("id:settings")
	app.Press("id:push-settings")
	app.Press("id:push-disable")
	r = awaitRequest(t, app, sent, "push")
	if err := json.Unmarshal(r.Push, &registration); err != nil || !registration.Disabled {
		t.Fatal("missing disable registration", string(r.Push))
	}
}
func TestPhonePushDeniedAndMissingNativeRegistration(t *testing.T) {
	app, m, sent := startPhone(t)
	app.NotificationAnswer = core.NotificationsDenied
	openPhoneDrawer(app)
	app.Press("id:settings")
	app.Press("id:push-settings")
	app.Press("id:push-authorize")
	if m.push.granted || m.push.err != tr("Allow Miya notifications in Settings") {
		t.Fatal("denial not surfaced")
	}
	app.NotificationPermission = core.NotificationsGranted
	app.Press("id:push-authorize")
	if m.push.err != tr("Remote notifications are unavailable in this app build.") {
		t.Fatal("native gap not surfaced", m.push.err)
	}
	select {
	case r := <-sent:
		if r.Op == "push" {
			t.Fatal("registered without token", r)
		}
	default:
	}
}
func TestPhoneWebServiceSheetOwnsVerifiedProxy(t *testing.T) {
	for _, closeBy := range []string{"back", "background", "disconnect", "navigation"} {
		t.Run(closeBy, func(t *testing.T) {
			app, m, sent := startPhone(t)
			openPhoneThread(t, app, m, sent)
			closed := false
			changePhone(app, m, func() {
				m.State.Runtime, m.State.WebPorts = true, true
				m.fetchRuntime = func(_ context.Context, _ *remote.Conn, _ string, ref model.ThreadRef) (tracker.ConversationRuntime, error) {
					return tracker.ConversationRuntime{View: rt.View{Key: tracker.KeyOf(ref)}, Services: []webservice.Service{{Host: "127.0.0.1", Port: 3000, RuntimeToken: "verified"}}}, nil
				}
				m.openWebProxy = func(_ *remote.Conn, service webservice.Service) (string, func(), error) {
					if service.RuntimeToken != "verified" {
						t.Error("lost runtime token")
					}
					return "http://127.0.0.1:12345/auth/", func() { closed = true }, nil
				}
			})
			choosePhoneAction(t, app, "Web services")
			app.WaitFor("id:web-service-3000", time.Second)
			app.Press("id:web-service-3000")
			if app.Find("id:web-service-webview") == nil {
				t.Fatal("no webview")
			}
			switch closeBy {
			case "back":
				app.Press("id:close-files")
			case "background":
				app.SetEnvironment(core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone, Lifecycle: core.LifecycleBackground})
			case "disconnect":
				changePhone(app, m, m.Disconnect)
			case "navigation":
				changePhone(app, m, m.Home)
			}
			if !closed || m.filesURL.Get() != "" {
				t.Fatal("proxy survived", closeBy)
			}
		})
	}
}
func TestPhoneWebServicesRejectStaleResultsAndClearFailedRefresh(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	var fail bool
	changePhone(app, m, func() {
		m.State.Runtime, m.State.WebPorts = true, true
		m.fetchRuntime = func(_ context.Context, _ *remote.Conn, _ string, ref model.ThreadRef) (tracker.ConversationRuntime, error) {
			if fail {
				return tracker.ConversationRuntime{}, errors.New("lost observation")
			}
			return tracker.ConversationRuntime{View: rt.View{Key: tracker.KeyOf(ref)}, Services: []webservice.Service{{Host: "127.0.0.1", Port: 8080, RuntimeToken: "token"}}}, nil
		}
	})
	choosePhoneAction(t, app, "Web services")
	app.WaitFor("id:web-service-8080", time.Second)
	fail = true
	app.Press("id:refresh-web")
	app.WaitForGone("id:web-service-8080", time.Second)
	app.WaitUntil(time.Second, func() bool { return m.web != nil && !m.web.loading })
	if m.web.err == "" {
		t.Fatal("failed refresh retained services")
	}
	// A response belonging to the previous sheet must not revive it.
	entered, release := make(chan struct{}), make(chan struct{})
	changePhone(app, m, func() {
		m.fetchRuntime = func(_ context.Context, _ *remote.Conn, _ string, ref model.ThreadRef) (tracker.ConversationRuntime, error) {
			close(entered)
			<-release
			return tracker.ConversationRuntime{View: rt.View{Key: tracker.KeyOf(ref)}}, nil
		}
	})
	app.Press("id:refresh-web")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("no fetch")
	}
	app.Back()
	close(release)
	app.Settle()
	if m.web != nil || m.pages.Top() != "" {
		t.Fatal("stale result reopened sheet")
	}
}

func TestPhoneWebServicesSwitchScopeAndOpenManualPort(t *testing.T) {
	app, m, sent := startPhone(t)
	openPhoneThread(t, app, m, sent)
	var opened webservice.Service
	changePhone(app, m, func() {
		m.State.Runtime, m.State.WebPorts = true, true
		m.fetchRuntime = func(_ context.Context, _ *remote.Conn, _ string, ref model.ThreadRef) (tracker.ConversationRuntime, error) {
			return tracker.ConversationRuntime{View: rt.View{Key: tracker.KeyOf(ref),
				Tasks:     []rt.Task{{Description: "Watch assets", Status: "running"}},
				Processes: []rt.Process{{Name: "node", Identity: rt.Identity{PID: 123}}}}}, nil
		}
		m.fetchProjectServices = func(_ context.Context, _ *remote.Conn, project string) ([]webservice.Service, error) {
			if project != "/work" {
				t.Errorf("requested project %q", project)
			}
			return []webservice.Service{{Host: "127.0.0.1", Port: 4321}, {Host: "external.example", Port: 4322}}, nil
		}
		m.openWebProxy = func(_ *remote.Conn, service webservice.Service) (string, func(), error) {
			opened = service
			return "http://127.0.0.1:12345/auth/", func() {}, nil
		}
	})
	choosePhoneAction(t, app, "Web services")
	app.WaitFor("id:web-runtime", time.Second)
	app.Press("id:web-runtime")
	if !app.Exists("text:Watch assets") || !app.Exists("text:node · PID 123") {
		t.Fatal("runtime details missing", app.Texts())
	}
	app.Press("id:web-scope")
	app.WaitFor("id:web-service-4321", time.Second)
	if app.Exists("id:web-service-4322") || app.Exists("text:Watch assets") {
		t.Fatal("project overview retained unrelated observations", app.Texts())
	}
	app.Press("id:web-manual")
	app.Type("id:web-manual-port", "65536")
	if app.Enabled("id:web-manual-open") {
		t.Fatal("out-of-range port can be opened")
	}
	app.Clear("id:web-manual-port")
	app.Type("id:web-manual-port", "8443")
	app.Press("id:web-manual-open")
	if opened.Port != 8443 || opened.Host != "" || opened.RuntimeToken != "" || !app.Exists("id:web-service-webview") {
		t.Fatalf("unexpected manual endpoint: %+v", opened)
	}
}

func TestPhoneAndroidPushUsesLiveConnectionAndOptionalForegroundLease(t *testing.T) {
	previous := PlatformHooks
	defer func() { PlatformHooks = previous }()
	var released atomic.Int32
	PlatformHooks.KeepRunning = func(string) func() { return func() { released.Add(1) } }
	app, m, sent := startPhone(t)
	app.SetEnvironment(core.Environment{OS: core.OSAndroid, Idiom: core.IdiomPhone, Lifecycle: core.LifecycleActive})
	openPhoneDrawer(app)
	app.Press("id:settings")
	app.Press("id:push-settings")
	app.Press("id:push-authorize")
	r := awaitRequest(t, app, sent, "push")
	var registration notification.Registration
	if err := json.Unmarshal(r.Push, &registration); err != nil || !registration.Live || registration.Token != "" {
		t.Fatal("Android registration", string(r.Push))
	}
	if !m.KeepConnectionInBackground {
		t.Fatal("native lease was not acquired")
	}
	app.SetEnvironment(core.Environment{OS: core.OSAndroid, Idiom: core.IdiomPhone, Lifecycle: core.LifecycleBackground})
	if !m.Connected {
		t.Fatal("native lease did not preserve paired connection")
	}
	changePhone(app, m, func() { m.push.disabled = true; m.syncPush() })
	if released.Load() != 1 || m.KeepConnectionInBackground {
		t.Fatal("lease not released on disable")
	}
}

func TestPhonePushRegistrationErrorCanRetry(t *testing.T) {
	previous := PlatformHooks
	defer func() { PlatformHooks = previous }()
	var calls atomic.Int32
	PlatformHooks.RegisterPush = func(context.Context) (string, error) {
		if calls.Add(1) == 1 {
			return "", errors.New("registration denied")
		}
		return strings.Repeat("b", 64), nil
	}
	app, m, sent := startPhone(t)
	openPhoneDrawer(app)
	app.Press("id:settings")
	app.Press("id:push-settings")
	app.Press("id:push-authorize")
	app.WaitUntil(time.Second, func() bool {
		return !m.push.requesting && m.push.err == tr("Unable to register for notifications. Check system notification settings.")
	})
	app.Press("id:push-authorize")
	r := awaitRequest(t, app, sent, "push")
	var registration notification.Registration
	if err := json.Unmarshal(r.Push, &registration); err != nil || registration.Token != strings.Repeat("b", 64) {
		t.Fatal("retry", string(r.Push))
	}
}

func TestPhoneLiveActivityURLWaitsForMatchingDesktopState(t *testing.T) {
	app, m, sent := startPhone(t)
	source := m.State.ReferenceSource
	ref := model.ThreadRef{Source: source, Backend: "codex", SessionID: "session"}
	changePhone(app, m, func() { m.State.ReferenceSource = ""; m.openActivityURL(ref.URL()) })
	if m.Open != 0 {
		t.Fatal("activity opened before identity was known")
	}
	changePhone(app, m, func() { m.State.ReferenceSource = source })
	awaitRequest(t, app, sent, "subscribe")
	if m.Open != 7 {
		t.Fatal("activity tap did not route to its conversation")
	}
}
