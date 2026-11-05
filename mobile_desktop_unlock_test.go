package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"miya/internal/desktop"
)

func unlockClient(t *testing.T, status int, receive func(string)) *desktopClient {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == desktopControlPrefix+"/stop" && r.Method == "POST" {
			w.WriteHeader(204)
			return
		}
		if r.URL.Path != desktopControlPrefix+"/unlock" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test" {
			t.Error("invalid unlock request")
		}
		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if receive != nil {
			receive(body.Password)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	client := newDesktopClient(func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}, "test")
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func TestDesktopUnlockSavedPasswordAndHostIsolation(t *testing.T) {
	m := testMobileApp()
	store := newDesktopTestStore()
	key, other := desktopUnlockKey("mac-a"), desktopUnlockKey("mac-b")
	_ = store.Set(other, []byte("other-password"))
	var authenticated atomic.Bool
	m.desktopAuthenticate = func(context.Context) error { authenticated.Store(true); return nil }
	client := unlockClient(t, 204, func(password string) {
		if !authenticated.Load() || password != "test-password" {
			t.Error("unlock not authenticated or wrong password")
		}
	})
	result := m.desktopPerformUnlock(context.Background(), client, store, key, "test-password", false, true)
	if result.err != nil || !result.saved || !result.savedChanged {
		t.Fatal("password not saved after success", result.err)
	}
	data, err := store.Get(key)
	if err != nil || string(data) != "test-password" {
		t.Fatal("missing saved password")
	}
	authenticated.Store(false)
	result = m.desktopPerformUnlock(context.Background(), client, store, key, "", true, false)
	if result.err != nil || !authenticated.Load() {
		t.Fatal("saved unlock failed", result.err)
	}
	data, err = store.Get(other)
	if err != nil || string(data) != "other-password" {
		t.Fatal("another Mac password changed")
	}
	if desktopUnlockKey("") != "" {
		t.Fatal("empty host must not get a shared key")
	}
}

func TestDesktopUnlockCanceledAuthenticationSendsNothing(t *testing.T) {
	for _, saved := range []bool{false, true} {
		m := testMobileApp()
		store := newDesktopTestStore()
		key := desktopUnlockKey("mac")
		if saved {
			_ = store.Set(key, []byte("existing"))
		}
		m.desktopAuthenticate = func(context.Context) error { return errors.New("canceled") }
		var calls atomic.Int32
		client := unlockClient(t, 204, func(string) { calls.Add(1) })
		result := m.desktopPerformUnlock(context.Background(), client, store, key, "new", saved, true)
		if result.err == nil || calls.Load() != 0 {
			t.Fatal("canceled authentication sent a password")
		}
		data, err := store.Get(key)
		if saved && (err != nil || string(data) != "existing") || !saved && !errors.Is(err, mygo.ErrSecretNotFound) {
			t.Fatal("canceled authentication changed storage")
		}
	}
}

func TestDesktopUnlockCancellationAfterAuthentication(t *testing.T) {
	m := testMobileApp()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.desktopAuthenticate = func(context.Context) error { cancel(); return nil }
	var calls atomic.Int32
	client := unlockClient(t, 204, func(string) { calls.Add(1) })
	store := newDesktopTestStore()
	result := m.desktopPerformUnlock(ctx, client, store, "key", "secret", false, true)
	if !errors.Is(result.err, context.Canceled) || calls.Load() != 0 {
		t.Fatal("closed page sent password")
	}
	if _, err := store.Get("key"); !errors.Is(err, mygo.ErrSecretNotFound) {
		t.Fatal("closed page saved password")
	}
}

func TestDesktopUnlockOnlySuccessfulPasswordSaved(t *testing.T) {
	for _, status := range []int{401, 403, 409, 410, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			m := testMobileApp()
			m.desktopAuthenticate = func(context.Context) error { return nil }
			store := newDesktopTestStore()
			result := m.desktopPerformUnlock(context.Background(), unlockClient(t, status, nil), store, "key", "test", false, true)
			if desktopStatus(result.err) != status {
				t.Fatal(result.err)
			}
			if _, err := store.Get("key"); !errors.Is(err, mygo.ErrSecretNotFound) {
				t.Fatal("unverified password saved")
			}
		})
	}
}

func TestDesktopUnlockIncorrectSavedPasswordForgotten(t *testing.T) {
	m := testMobileApp()
	m.desktopAuthenticate = func(context.Context) error { return nil }
	store := newDesktopTestStore()
	_ = store.Set("key", []byte("incorrect"))
	result := m.desktopPerformUnlock(context.Background(), unlockClient(t, 401, nil), store, "key", "", true, false)
	if desktopStatus(result.err) != 401 || !result.savedChanged || result.saved {
		t.Fatal("incorrect password retained")
	}
	if _, err := store.Get("key"); !errors.Is(err, mygo.ErrSecretNotFound) {
		t.Fatal("incorrect password still stored")
	}
}

func TestDesktopUnlockStorageFailureAndManualFallback(t *testing.T) {
	m := testMobileApp()
	m.desktopAuthenticate = func(context.Context) error { t.Error("manual unlock asked for authentication"); return nil }
	client := unlockClient(t, 204, nil)
	if result := m.desktopPerformUnlock(context.Background(), client, nil, "", "manual", false, false); result.err != nil {
		t.Fatal("manual unlock needs storage", result.err)
	}
	m.desktopAuthenticate = func(context.Context) error { return nil }
	store := newDesktopTestStore()
	store.fail = true
	result := m.desktopPerformUnlock(context.Background(), client, store, "key", "test", false, true)
	if result.err != nil || !result.storageError || result.saved {
		t.Fatal("save failure misreported")
	}
}

// Queue UI callbacks so only the test goroutine reads or updates page state.
func unlockUI(t *testing.T) (chan func(), func(func() bool)) {
	t.Helper()
	callbacks := make(chan func(), 64)
	old := runOnMain
	runOnMain = func(f func()) { callbacks <- f }
	t.Cleanup(func() { runOnMain = old })
	return callbacks, func(done func() bool) {
		deadline := time.After(3 * time.Second)
		for !done() {
			select {
			case f := <-callbacks:
				f()
			case <-deadline:
				t.Fatal("unlock UI did not settle")
			}
		}
	}
}

func TestDesktopUnlockAutoOnceAndForgetUI(t *testing.T) {
	_, wait := unlockUI(t)
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.phase = "live"
	p.unlock.key = desktopUnlockKey("mac")
	store := newDesktopTestStore()
	m.store = store
	_ = store.Set(p.unlock.key, []byte("saved"))
	var auth atomic.Int32
	m.desktopAuthenticate = func(context.Context) error { auth.Add(1); return errors.New("canceled") }
	m.desktopCheckSavedUnlock(p)
	m.desktopLockChanged(p, true)
	wait(func() bool { return p.unlock.saved && !p.unlock.busy && auth.Load() == 1 })
	for range 5 {
		m.desktopLockChanged(p, true)
	}
	if auth.Load() != 1 || p.unlock.busy {
		t.Fatal("automatic unlock repeated after cancel")
	}
	m.desktopLockChanged(p, false)
	m.desktopLockChanged(p, true)
	wait(func() bool { return !p.unlock.busy && auth.Load() == 2 })
	m.desktopForgetUnlock(p)
	wait(func() bool { return !p.unlock.busy && !p.unlock.saved })
	if _, err := store.Get(p.unlock.key); !errors.Is(err, mygo.ErrSecretNotFound) {
		t.Fatal("forget did not delete key")
	}
}

func TestDesktopUnlockAuthorizationPageAndOrdinaryInput(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.windowOnly, p.unlock.saved, p.unlock.key = true, true, "key"
	m.desktopAuthenticate = func(context.Context) error { t.Error("authorization page used saved password"); return nil }
	m.desktopLockChanged(p, true)
	if p.unlock.locked || p.unlock.busy {
		t.Fatal("authorization lease offered unlock")
	}
	p.pickable = true
	p.unlock.saved = false
	p.queue = []desktop.Input{{Kind: "text", Text: "pending"}}
	m.desktopLockChanged(p, true)
	m.desktopSend(p, desktop.Input{Kind: "text", Text: "ordinary"})
	if len(p.queue) != 0 {
		t.Fatal("ordinary input mixed with unlock")
	}
}

func TestDesktopUnlockLayout(t *testing.T) {
	for _, width := range []int{320, 390} {
		for _, saved := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-saved-%v", width, saved), func(t *testing.T) {
				m := testMobileApp()
				m.store = newDesktopTestStore()
				p := liveTestDesktopPage(t, m)
				p.phase, p.status, p.image = "live", "Mac is locked", nil
				p.unlock = desktopUnlockState{locked: true, key: "mac", saved: saved, status: "Identity verification incomplete. Unlock manually or retry the saved password."}
				m.router().Push("/desktop")
				tt := ui.NewTester(m.viewRoot, width, 700)
				tt.Frame()
				tt.Frame()
				for _, text := range []string{"Mac is locked", "Unlock", "Save password for automatic unlock after identity verification"} {
					if !tt.HasText(text) {
						t.Fatal("missing", text)
					}
				}
				if tt.HasText("Send") {
					t.Fatal("ordinary input visible while locked")
				}
				labels := []string{"Mac login password", "Unlock"}
				if saved {
					labels = append(labels, "Verify and unlock", "Forget password")
				}
				for _, label := range labels {
					node, ok := tt.Find(label)
					if !ok || node.Y < 0 || node.Y+node.H > 700 || node.X < 0 || node.X+node.W > float32(width)+1 {
						t.Fatalf("%s outside phone viewport: %+v", label, node)
					}
				}
				saveMobileNativeSnapshot(t, tt, fmt.Sprintf("desktop-unlock-%d-saved-%v", width, saved))
				tt.SetSize(width, 440) // Remaining content height above the phone keyboard.
				tt.SetPreferences(ui.Preferences{TextScale: 1.6})
				tt.Frame()
				for _, label := range labels {
					r, ok := tt.Find(label)
					if !ok || r.Y < 0 || r.Y+r.H > 440 || r.X < 0 || r.X+r.W > float32(width)+1 {
						t.Fatalf("keyboard and large text hide %s: %v", label, r)
					}
				}
				saveMobileNativeSnapshot(t, tt, fmt.Sprintf("desktop-unlock-keyboard-%d-saved-%v", width, saved))
			})
		}
	}
}

func TestDesktopUnlockManualSaveUIAndClose(t *testing.T) {
	_, wait := unlockUI(t)
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.phase = "live"
	m.store = newDesktopTestStore()
	m.desktopAuthenticate = func(context.Context) error { return nil }
	p.client = unlockClient(t, 204, nil)
	p.unlock = desktopUnlockState{key: "mac", locked: true, text: "synthetic-password", remember: true}
	m.desktopUnlock(p)
	if p.unlock.text != "" || !p.unlock.busy {
		t.Fatal("submitted password stayed in the UI")
	}
	wait(func() bool { return !p.unlock.busy })
	if p.unlock.locked || !p.unlock.saved {
		t.Fatal("successful unlock did not update saved state")
	}
	p.unlock.text = "unsubmitted"
	m.closeDesktopPage(false)
	if p.unlock.text != "" || p.ctx.Err() == nil {
		t.Fatal("closed page retained typed credentials")
	}
}

func TestDesktopUnlockClosedPageCancelsAuthentication(t *testing.T) {
	m := testMobileApp()
	p := liveTestDesktopPage(t, m)
	p.phase = "live"
	m.store = newDesktopTestStore()
	entered, done := make(chan struct{}), make(chan struct{})
	m.desktopAuthenticate = func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		close(done)
		return ctx.Err()
	}
	p.unlock = desktopUnlockState{key: "mac", locked: true, text: "synthetic-password", remember: true}
	m.desktopUnlock(p)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("authentication did not start")
	}
	m.closeDesktopPage(false)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("authentication survived closed page")
	}
}

func TestDesktopUnlockRemovePairingClearsOnlyItsPassword(t *testing.T) {
	m := testMobileApp()
	store := newDesktopTestStore()
	m.store = store
	item, err := m.desktops.remember(testDesktopLink(), "removed")
	if err != nil {
		t.Fatal(err)
	}
	key := desktopUnlockKey(item.ID)
	_ = store.Set(key, []byte("removed"))
	_ = store.Set(desktopUnlockKey("other"), []byte("retained"))
	m.removeDesktop(item.ID)
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := store.Get(key); errors.Is(err, mygo.ErrSecretNotFound) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("removed pairing retained saved password")
		}
		time.Sleep(time.Millisecond)
	}
	if data, err := store.Get(desktopUnlockKey("other")); err != nil || string(data) != "retained" {
		t.Fatal("removed another desktop password")
	}
	// Drain the normal registry writer before this test returns.
	waitDesktopSave(t, m, store)
}

// The native MyGo bridge compiles authentication to ErrUnsupported unless the
// capability is declared; a purpose string alone does not enable Face ID.
func TestDesktopUnlockIOSAuthenticationCapability(t *testing.T) {
	data, err := os.ReadFile("mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		IOS struct {
			Capabilities []string       `json:"capabilities"`
			InfoPlist    map[string]any `json:"infoPlist"`
		} `json:"ios"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(config.IOS.Capabilities, "biometrics") {
		t.Fatal("remote unlock requires the native iOS biometrics capability")
	}
	purpose, _ := config.IOS.InfoPlist["NSFaceIDUsageDescription"].(string)
	if strings.TrimSpace(purpose) == "" {
		t.Fatal("remote unlock requires a Face ID purpose string")
	}
}

func TestDesktopUnlockAuthenticationFailureIsActionable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"unsupported", mygo.ErrUnsupported, "app build"},
		{"purpose", &mygo.AuthenticationError{Code: mygo.AuthenticationMissingPurpose}, "app build"},
		{"passcode", &mygo.AuthenticationError{Code: mygo.AuthenticationPasscodeNotSet}, "device passcode"},
		{"inactive", &mygo.AuthenticationError{Code: mygo.AuthenticationInactive}, "Return to Miya"},
		{"busy", &mygo.AuthenticationError{Code: mygo.AuthenticationBusy}, "Return to Miya"},
		{"failed", &mygo.AuthenticationError{Code: mygo.AuthenticationFailed}, "verification failed"},
		{"lockedout", &mygo.AuthenticationError{Code: mygo.AuthenticationLockedOut}, "iPhone Settings"},
		{"timeout", context.DeadlineExceeded, "timed out"},
		{"canceled", &mygo.AuthenticationError{Code: mygo.AuthenticationCanceled}, "incomplete"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testMobileApp()
			m.desktopAuthenticate = func(context.Context) error { return tc.err }
			var calls atomic.Int32
			client := unlockClient(t, 204, func(string) { calls.Add(1) })
			store := newDesktopTestStore()
			result := m.desktopPerformUnlock(context.Background(), client, store, "key", "synthetic", false, true)
			if result.err == nil || !strings.Contains(result.err.Error(), tc.want) || calls.Load() != 0 {
				t.Fatalf("authentication result = %v; requests = %d", result.err, calls.Load())
			}
			if _, err := store.Get("key"); !errors.Is(err, mygo.ErrSecretNotFound) {
				t.Fatal("failed authentication saved password")
			}
		})
	}
}
