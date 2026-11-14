//go:build !mygo_ui

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"miya/internal/desktop/control"
	mobile "miya/internal/phone/client"
	"miya/internal/uiruntime"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func unlockClient(t *testing.T, status int, receive func(string)) *control.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == control.Prefix+"/stop" && r.Method == "POST" {
			w.WriteHeader(204)
			return
		}
		if r.URL.Path != control.Prefix+"/unlock" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test" {
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
	client := control.NewClient(func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}, "test")
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func TestDesktopUnlockSavedPasswordAndHostIsolation(t *testing.T) {
	m := testPhoneClient()
	store := newDesktopTestStore()
	key, other := mobile.DesktopUnlockKey("mac-a"), mobile.DesktopUnlockKey("mac-b")
	_ = store.Set(other, []byte("other-password"))
	var authenticated atomic.Bool
	m.DesktopAuthenticate = func(context.Context) error { authenticated.Store(true); return nil }
	client := unlockClient(t, 204, func(password string) {
		if !authenticated.Load() || password != "test-password" {
			t.Error("unlock not authenticated or wrong password")
		}
	})
	result := m.DesktopPerformUnlock(context.Background(), client, store, key, "test-password", false, true)
	if result.Err != nil || !result.Saved || !result.SavedChanged {
		t.Fatal("password not saved after success", result.Err)
	}
	data, err := store.Get(key)
	if err != nil || string(data) != "test-password" {
		t.Fatal("missing saved password")
	}
	authenticated.Store(false)
	result = m.DesktopPerformUnlock(context.Background(), client, store, key, "", true, false)
	if result.Err != nil || !authenticated.Load() {
		t.Fatal("saved unlock failed", result.Err)
	}
	data, err = store.Get(other)
	if err != nil || string(data) != "other-password" {
		t.Fatal("another Mac password changed")
	}
	if mobile.DesktopUnlockKey("") != "" {
		t.Fatal("empty host must not get a shared key")
	}
}

func TestDesktopUnlockCanceledAuthenticationSendsNothing(t *testing.T) {
	for _, saved := range []bool{false, true} {
		m := testPhoneClient()
		store := newDesktopTestStore()
		key := mobile.DesktopUnlockKey("mac")
		if saved {
			_ = store.Set(key, []byte("existing"))
		}
		m.DesktopAuthenticate = func(context.Context) error { return errors.New("canceled") }
		var calls atomic.Int32
		client := unlockClient(t, 204, func(string) { calls.Add(1) })
		result := m.DesktopPerformUnlock(context.Background(), client, store, key, "new", saved, true)
		if result.Err == nil || calls.Load() != 0 {
			t.Fatal("canceled authentication sent a password")
		}
		data, err := store.Get(key)
		if saved && (err != nil || string(data) != "existing") || !saved && !errors.Is(err, testSecretNotFound) {
			t.Fatal("canceled authentication changed storage")
		}
	}
}

func TestDesktopUnlockCancellationAfterAuthentication(t *testing.T) {
	m := testPhoneClient()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.DesktopAuthenticate = func(context.Context) error { cancel(); return nil }
	var calls atomic.Int32
	client := unlockClient(t, 204, func(string) { calls.Add(1) })
	store := newDesktopTestStore()
	result := m.DesktopPerformUnlock(ctx, client, store, "key", "secret", false, true)
	if !errors.Is(result.Err, context.Canceled) || calls.Load() != 0 {
		t.Fatal("closed page sent password")
	}
	if _, err := store.Get("key"); !errors.Is(err, testSecretNotFound) {
		t.Fatal("closed page saved password")
	}
}

func TestDesktopUnlockOnlySuccessfulPasswordSaved(t *testing.T) {
	for _, status := range []int{401, 403, 409, 410, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			m := testPhoneClient()
			m.DesktopAuthenticate = func(context.Context) error { return nil }
			store := newDesktopTestStore()
			result := m.DesktopPerformUnlock(context.Background(), unlockClient(t, status, nil), store, "key", "test", false, true)
			if control.Status(result.Err) != status {
				t.Fatal(result.Err)
			}
			if _, err := store.Get("key"); !errors.Is(err, testSecretNotFound) {
				t.Fatal("unverified password saved")
			}
		})
	}
}

func TestDesktopUnlockIncorrectSavedPasswordForgotten(t *testing.T) {
	m := testPhoneClient()
	m.DesktopAuthenticate = func(context.Context) error { return nil }
	store := newDesktopTestStore()
	_ = store.Set("key", []byte("incorrect"))
	result := m.DesktopPerformUnlock(context.Background(), unlockClient(t, 401, nil), store, "key", "", true, false)
	if control.Status(result.Err) != 401 || !result.SavedChanged || result.Saved {
		t.Fatal("incorrect password retained")
	}
	if _, err := store.Get("key"); !errors.Is(err, testSecretNotFound) {
		t.Fatal("incorrect password still stored")
	}
}

func TestDesktopUnlockStorageFailureAndManualFallback(t *testing.T) {
	m := testPhoneClient()
	m.DesktopAuthenticate = func(context.Context) error { t.Error("manual unlock asked for authentication"); return nil }
	client := unlockClient(t, 204, nil)
	if result := m.DesktopPerformUnlock(context.Background(), client, nil, "", "manual", false, false); result.Err != nil {
		t.Fatal("manual unlock needs storage", result.Err)
	}
	m.DesktopAuthenticate = func(context.Context) error { return nil }
	store := newDesktopTestStore()
	store.fail = true
	result := m.DesktopPerformUnlock(context.Background(), client, store, "key", "test", false, true)
	if result.Err != nil || !result.StorageError || result.Saved {
		t.Fatal("save failure misreported")
	}
}

// Queue UI callbacks so only the test goroutine reads or updates page state.
func unlockUI(t *testing.T) (chan func(), func(func() bool)) {
	t.Helper()
	callbacks := make(chan func(), 64)
	old := uiruntime.RunOnMain
	uiruntime.RunOnMain = func(f func()) { callbacks <- f }
	t.Cleanup(func() { uiruntime.RunOnMain = old })
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

func TestDesktopUnlockRemovePairingClearsOnlyItsPassword(t *testing.T) {
	m := testPhoneClient()
	store := newDesktopTestStore()
	m.Store = store
	item, err := m.Desktops.Remember(testDesktopLink(), "removed")
	if err != nil {
		t.Fatal(err)
	}
	key := mobile.DesktopUnlockKey(item.ID)
	_ = store.Set(key, []byte("removed"))
	_ = store.Set(mobile.DesktopUnlockKey("other"), []byte("retained"))
	m.RemoveDesktop(item.ID)
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := store.Get(key); errors.Is(err, testSecretNotFound) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("removed pairing retained saved password")
		}
		time.Sleep(time.Millisecond)
	}
	if data, err := store.Get(mobile.DesktopUnlockKey("other")); err != nil || string(data) != "retained" {
		t.Fatal("removed another desktop password")
	}
	// Drain the normal registry writer before this test returns.
	waitDesktopSave(t, m, store)
}
