package nsmobile

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	phonecache "miya/internal/phone/cache"
	phoneclient "miya/internal/phone/client"
)

func authenticatedDesktop(t *testing.T, os core.OS) (*nsgotest.App, *App, *phoneclient.MobileDesktopPage) {
	t.Helper()
	if nsgotest.Target() != "" {
		t.Skip("headless authentication model")
	}
	app, m, p, _ := startDesktop(t, "live")
	app.SetEnvironment(core.Environment{OS: os, Idiom: core.IdiomPhone, Locale: "en"})
	app.BiometricAvailability = core.BiometricFace
	changePhone(app, m, func() {
		p.Unlock = phoneclient.DesktopUnlockState{Key: phoneclient.DesktopUnlockKey("test-mac"), Locked: true}
		m.refreshDesktopAuthentication()
	})
	return app, m, p
}

func TestNativeDesktopPasswordSaveAndUse(t *testing.T) {
	for _, os := range []core.OS{core.OSIOS, core.OSAndroid} {
		t.Run(string(os), func(t *testing.T) {
			app, m, p := authenticatedDesktop(t, os)
			const password = "synthetic-password"
			key := [2]string{"Miya", p.Unlock.Key}
			app.BiometricAnswer = core.BiometricSuccess
			var submissions atomic.Int32
			p.Client.SetTransport(desktopTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/unlock") {
					var body struct{ Password string }
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Password != password {
						t.Error("wrong unlock payload", err)
					}
					submissions.Add(1)
				}
				return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
			}))
			app.Press("id:desktop-unlock-save")
			app.Type("id:desktop-unlock-password", password)
			app.Press("id:desktop-unlock")
			if p.Unlock.Text != "" {
				t.Fatal("submitted password retained by input")
			}
			settleNative(t, app, func() bool { return !p.Unlock.Busy })
			if p.Unlock.Locked || !p.Unlock.Saved || len(app.AuthenticationReasons) != 1 || submissions.Load() != 1 {
				t.Fatal("authenticated save did not unlock", p.Unlock, app.AuthenticationReasons, submissions.Load())
			}
			if testSecrets(app)[key] != base64.StdEncoding.EncodeToString([]byte(password)) {
				t.Fatal("password not saved in native secure storage")
			}
			if v, _ := app.Storage().GetItem(p.Unlock.Key); v != "" {
				t.Fatal("password leaked into preferences")
			}
			changePhone(app, m, func() { m.DesktopLockChanged(p, false); m.DesktopLockChanged(p, true) })
			settleNative(t, app, func() bool { return !p.Unlock.Busy })
			if p.Unlock.Locked || len(app.AuthenticationReasons) != 2 || submissions.Load() != 2 || p.Unlock.Text != "" {
				t.Fatal("automatic saved-password use did not freshly authenticate")
			}
		})
	}
}

type authenticationStore struct {
	phonecache.SecretStore
	reads, writes atomic.Int32
}

func (s *authenticationStore) Get(key string) ([]byte, error) {
	s.reads.Add(1)
	return s.SecretStore.Get(key)
}
func (s *authenticationStore) Set(key string, data []byte) error {
	s.writes.Add(1)
	return s.SecretStore.Set(key, data)
}

func TestNativeDesktopAuthenticationRejectsCancelFailureAndUnavailability(t *testing.T) {
	for _, tc := range []struct {
		name      string
		available bool
		ok        bool
		err       error
	}{
		{"cancel", true, false, nil},
		{"failure", true, false, errors.New("native failure detail")},
		{"success with error", true, true, errors.New("native failure detail")},
		{"unavailable", false, true, nil},
	} {
		for _, saved := range []bool{false, true} {
			name := tc.name + "/save"
			if saved {
				name = tc.name + "/use"
			}
			t.Run(name, func(t *testing.T) {
				app, m, p := authenticatedDesktop(t, core.OSIOS)
				key := [2]string{"Miya", p.Unlock.Key}
				if saved {
					_ = app.Secrets.WriteSecret(testSecretItem(key), base64.StdEncoding.EncodeToString([]byte("saved-password")))
				}
				original := testSecrets(app)[key]
				if !tc.available {
					app.BiometricAvailability = core.BiometricNone
				}
				app.BiometricAnswer = core.BiometricCancelled
				if tc.err != nil {
					app.BiometricAnswer = core.BiometricFailure
				}
				store := &authenticationStore{SecretStore: m.Store}
				var submissions atomic.Int32
				p.Client.SetTransport(desktopTransport(func(r *http.Request) (*http.Response, error) {
					if strings.HasSuffix(r.URL.Path, "/unlock") {
						submissions.Add(1)
					}
					return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
				}))
				// Deliberately leave the UI's earlier availability cached as true:
				// each attempt must query the current native policy before prompting.
				nativeWorker(t, app, func() {
					result := m.DesktopPerformUnlock(context.Background(), p.Client, store, p.Unlock.Key, "typed-password", saved, !saved)
					if result.Err == nil || result.SavedChanged || strings.Contains(result.Err.Error(), "native failure detail") {
						t.Error("denial was accepted or exposed native details", result)
					}
					if !tc.available && result.Err.Error() != tr("Device authentication is unavailable. Check biometrics and your device passcode in system settings, or unlock manually without saving.") {
						t.Error("unavailability was not actionable", result.Err)
					}
				})
				if (len(app.AuthenticationReasons) == 1) != tc.available || submissions.Load() != 0 || store.reads.Load() != 0 || store.writes.Load() != 0 || testSecrets(app)[key] != original {
					t.Fatal("denied authentication used or changed a password", app.AuthenticationReasons, submissions.Load(), store.reads.Load(), store.writes.Load())
				}
			})
		}
	}
}

func TestNativeDesktopAuthenticationCanceledPageCannotUseLateSuccess(t *testing.T) {
	app, m, p := authenticatedDesktop(t, core.OSIOS)
	app.BiometricAnswer = core.BiometricSuccess
	m.CloseDesktopPage(false)
	store := &authenticationStore{SecretStore: m.Store}
	nativeWorker(t, app, func() {
		result := m.DesktopPerformUnlock(p.Ctx, nil, store, p.Unlock.Key, "typed-password", false, true)
		if result.Err == nil || result.SavedChanged {
			t.Error("late authentication success authorized a closed page")
		}
	})
	if store.reads.Load() != 0 || store.writes.Load() != 0 {
		t.Fatal("closed page accessed a password")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	nativeWorker(t, app, func() {
		if err := m.DesktopAuthenticate(ctx); !errors.Is(err, context.Canceled) {
			t.Error("canceled authentication did not return promptly", err)
		}
	})
}

func TestNativeDesktopAuthenticationAvailabilityUI(t *testing.T) {
	for _, tc := range []struct {
		os        core.OS
		kind      core.BiometricKind
		available bool
		label     string
	}{
		{core.OSIOS, core.BiometricFace, true, "Face ID"},
		{core.OSIOS, core.BiometricFingerprint, true, "Touch ID"},
		{core.OSAndroid, core.BiometricFace, true, "Face recognition or device screen lock"},
		{core.OSAndroid, core.BiometricFingerprint, true, "Fingerprint or device screen lock"},
		{core.OSIOS, core.BiometricFace, false, "Password saving requires device authentication, which is unavailable in this NSGo build."},
		{core.OSLinux, core.BiometricNone, false, "Password saving requires device authentication, which is unavailable in this NSGo build."},
	} {
		t.Run(string(tc.os)+"/"+tc.label, func(t *testing.T) {
			app, m, p := authenticatedDesktop(t, tc.os)
			app.BiometricAvailability = tc.kind
			if !tc.available {
				app.BiometricAvailability = core.BiometricNone
			}
			changePhone(app, m, func() { p.Unlock.Saved = true; m.refreshDesktopAuthentication() })
			if !strings.Contains(strings.Join(app.Texts(), " "), tc.label) || app.Exists("id:desktop-unlock-save") != tc.available || app.Exists("id:desktop-unlock-saved") != tc.available {
				t.Fatal("unlock availability/type incorrect", app.Texts())
			}
			if !app.Exists("id:desktop-unlock") {
				t.Fatal("manual unlock must remain available")
			}
			app.Press("id:desktop-more")
			if len(app.QueryMounted("id:desktop-forget-password")) != 1 {
				t.Fatal("forget must remain available in desktop options")
			}
			changePhone(app, m, func() { m.pages.Open("settings") })
			if !strings.Contains(strings.Join(app.Texts(), " "), tc.label) {
				t.Fatal("settings lost availability/type", app.Texts())
			}
			// Returning from system settings must update retained controls.
			changePhone(app, m, func() { p.Unlock.Remember = true })
			app.BiometricAvailability = core.BiometricNone
			app.SetEnvironment(core.Environment{OS: tc.os, Idiom: core.IdiomPhone, Locale: "en", Lifecycle: core.LifecycleInactive})
			app.SetEnvironment(core.Environment{OS: tc.os, Idiom: core.IdiomPhone, Locale: "en", Lifecycle: core.LifecycleActive})
			if m.authentication.available || p.Unlock.Remember {
				t.Fatal("foreground retained stale availability/save choice")
			}
		})
	}
}
