//go:build !mygo_ui

package integration

import (
	"context"
	"errors"
	"miya/internal/desktop"
	mobile "miya/internal/phone/client"
	"sync/atomic"
	"testing"
	"time"
)

func TestNSGoDesktopUnlockAutoOnceAndForgetUI(t *testing.T) {
	m := testPhoneClient()
	_, wait := unlockClientDispatch(t, m)
	p := livePhoneDesktopPage(t, m)
	p.Phase = "live"
	p.Unlock.Key = mobile.DesktopUnlockKey("mac")
	store := newDesktopTestStore()
	m.Store = store
	_ = store.Set(p.Unlock.Key, []byte("saved"))
	var auth atomic.Int32
	m.DesktopAuthenticate = func(context.Context) error { auth.Add(1); return errors.New("canceled") }
	m.DesktopCheckSavedUnlock(p)
	m.DesktopLockChanged(p, true)
	wait(func() bool { return p.Unlock.Saved && !p.Unlock.Busy && auth.Load() == 1 })
	for range 5 {
		m.DesktopLockChanged(p, true)
	}
	if auth.Load() != 1 || p.Unlock.Busy {
		t.Fatal("automatic unlock repeated after cancel")
	}
	m.DesktopLockChanged(p, false)
	m.DesktopLockChanged(p, true)
	wait(func() bool { return !p.Unlock.Busy && auth.Load() == 2 })
	m.DesktopForgetUnlock(p)
	wait(func() bool { return !p.Unlock.Busy && !p.Unlock.Saved })
	if _, err := store.Get(p.Unlock.Key); !errors.Is(err, testSecretNotFound) {
		t.Fatal("forget did not delete key")
	}
}

func TestNSGoDesktopUnlockAuthorizationPageAndOrdinaryInput(t *testing.T) {
	m := testPhoneClient()
	p := livePhoneDesktopPage(t, m)
	p.WindowOnly, p.Unlock.Saved, p.Unlock.Key = true, true, "key"
	m.DesktopAuthenticate = func(context.Context) error { t.Error("authorization page used saved password"); return nil }
	m.DesktopLockChanged(p, true)
	if p.Unlock.Locked || p.Unlock.Busy {
		t.Fatal("authorization lease offered unlock")
	}
	p.Pickable = true
	p.Unlock.Saved = false
	p.Queue = []desktop.Input{{Kind: "text", Text: "pending"}}
	m.DesktopLockChanged(p, true)
	m.DesktopSend(p, desktop.Input{Kind: "text", Text: "ordinary"})
	if len(p.Queue) != 0 {
		t.Fatal("ordinary input mixed with unlock")
	}
}

func TestNSGoDesktopUnlockManualSaveUIAndClose(t *testing.T) {
	m := testPhoneClient()
	_, wait := unlockClientDispatch(t, m)
	p := livePhoneDesktopPage(t, m)
	p.Phase = "live"
	m.Store = newDesktopTestStore()
	m.DesktopAuthenticate = func(context.Context) error { return nil }
	p.Client = unlockClient(t, 204, nil)
	p.Unlock = mobile.DesktopUnlockState{Key: "mac", Locked: true, Text: "synthetic-password", Remember: true}
	m.DesktopUnlock(p)
	if p.Unlock.Text != "" || !p.Unlock.Busy {
		t.Fatal("submitted password stayed in the UI")
	}
	wait(func() bool { return !p.Unlock.Busy })
	if p.Unlock.Locked || !p.Unlock.Saved {
		t.Fatal("successful unlock did not update saved state")
	}
	p.Unlock.Text = "unsubmitted"
	m.CloseDesktopPage(false)
	if p.Unlock.Text != "" || p.Ctx.Err() == nil {
		t.Fatal("closed page retained typed credentials")
	}
}

func TestNSGoDesktopUnlockClosedPageCancelsAuthentication(t *testing.T) {
	m := testPhoneClient()
	p := livePhoneDesktopPage(t, m)
	p.Phase = "live"
	m.Store = newDesktopTestStore()
	entered, done := make(chan struct{}), make(chan struct{})
	m.DesktopAuthenticate = func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		close(done)
		return ctx.Err()
	}
	p.Unlock = mobile.DesktopUnlockState{Key: "mac", Locked: true, Text: "synthetic-password", Remember: true}
	m.DesktopUnlock(p)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("authentication did not start")
	}
	m.CloseDesktopPage(false)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("authentication survived closed page")
	}
}

func unlockClientDispatch(t *testing.T, m *mobile.Client) (chan func(), func(func() bool)) {
	t.Helper()
	callbacks := make(chan func(), 64)
	m.Dispatch = func(f func()) { callbacks <- f }
	return callbacks, func(done func() bool) {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for !done() {
			select {
			case f := <-callbacks:
				f()
			case <-deadline:
				t.Fatal("unlock did not settle")
			}
		}
	}
}
