package client

import (
	"context"
	"errors"
	"time"

	"miya/internal/desktop/control"
	phonecache "miya/internal/phone/cache"
)

type DesktopUnlockState struct {
	revision                                          uint64
	Locked, Busy, Saved, checked, attempted, Remember bool
	Key, Text, Status                                 string
}

func DesktopUnlockKey(id string) string {
	if id == "" {
		return ""
	}
	return "desktop-unlock-v1-" + id
}

func (p *MobileDesktopPage) CanUnlock() bool { return !p.WindowOnly || p.Pickable }

func (m *Client) DesktopCheckSavedUnlock(p *MobileDesktopPage) {
	if !p.CanUnlock() || p.Unlock.checked || p.Unlock.Key == "" || m.Store == nil {
		return
	}
	p.Unlock.checked = true
	store, key, revision := m.Store, p.Unlock.Key, p.Unlock.revision
	m.desktopLeaseDo(p, func(ctx context.Context) func() {
		data, err := store.Get(key)
		saved := err == nil && len(data) > 0
		clear(data)
		return func() {
			if p.Unlock.revision != revision {
				return
			}
			p.Unlock.Saved = saved
			if err != nil && !errors.Is(err, m.SecretNotFound) && !p.Unlock.Busy {
				p.Unlock.Status = tr("Unable to read the saved password. Unlock manually.")
			}
			m.desktopAutoUnlock(p)
		}
	})
}

func (m *Client) DesktopLockChanged(p *MobileDesktopPage, locked bool) {
	if !p.CanUnlock() || p.Unlock.Busy {
		return
	}
	wasLocked := p.Unlock.Locked
	p.Unlock.Locked = locked
	if !locked {
		if wasLocked {
			p.Unlock.Status = ""
		}
		p.Unlock.Text, p.Unlock.attempted = "", false
		return
	}
	// Old queued clicks and text must not interleave with unlocking.
	p.Queue, p.Text, p.Image = nil, "", nil
	p.Mapping.Reset()
	m.desktopAutoUnlock(p)
}

func (m *Client) desktopAutoUnlock(p *MobileDesktopPage) {
	if p.Active && p.CanUnlock() && p.Unlock.Locked && p.Unlock.Saved && !p.Unlock.attempted && !p.Unlock.Busy {
		m.desktopBeginUnlock(p, true)
	}
}

func (m *Client) DesktopUnlockSaved(p *MobileDesktopPage) {
	if p.Unlock.Saved {
		m.desktopBeginUnlock(p, true)
	}
}

func (m *Client) DesktopUnlock(p *MobileDesktopPage) {
	m.desktopBeginUnlock(p, false)
}

func (m *Client) desktopBeginUnlock(p *MobileDesktopPage, saved bool) {
	if !p.Active || !p.CanUnlock() || !p.Unlock.Locked || p.Unlock.Busy || (!saved && !control.ValidUnlockPassword(p.Unlock.Text)) {
		return
	}
	password, remember := p.Unlock.Text, p.Unlock.Remember
	store, key := m.Store, p.Unlock.Key
	p.Unlock.Text, p.Unlock.Busy, p.Unlock.attempted = "", true, true
	p.Unlock.Status = tr("Unlocking Mac…")
	if saved || remember {
		p.Unlock.Status = tr("Verify your identity to use your Mac login password…")
	}
	m.desktopLeaseDo(p, func(ctx context.Context) func() {
		result := m.DesktopPerformUnlock(ctx, p.Client, store, key, password, saved, remember)
		password = ""
		return func() {
			p.Unlock.Busy = false
			if result.SavedChanged {
				p.Unlock.revision++
				p.Unlock.Saved = result.Saved
			}
			switch status := control.Status(result.Err); {
			case result.Err == nil, status == 409:
				p.Unlock.Locked, p.Unlock.Status = false, ""
			case status == 410:
				m.DesktopFinish(p, result.Err.Error())
			case status == 401:
				p.Unlock.Status = tr("The password did not unlock your Mac. Check it and retry manually.")
			default:
				p.Unlock.Status = result.Err.Error()
			}
			if result.StorageError {
				p.Unlock.Status = tr("Keychain operation failed. Password storage is unchanged.")
			}
		}
	})
}

type desktopUnlockResult struct {
	Err                               error
	SavedChanged, Saved, StorageError bool
}

// ErrDesktopAuthenticationUnavailable means device owner authentication cannot
// currently be attempted. The UI must keep manual unlock/forget available.
var ErrDesktopAuthenticationUnavailable = errors.New("device authentication unavailable")

func (m *Client) authenticateDesktopUnlock(ctx context.Context) error {
	if m.DesktopAuthenticate != nil {
		return m.DesktopAuthenticate(ctx)
	}
	return errors.New(tr("Identity verification is unavailable in this app build. Update Miya, or disable password saving to unlock manually."))
}

func desktopUnlockAuthenticationError(err error) error {
	if errors.Is(err, ErrDesktopAuthenticationUnavailable) {
		return errors.New(tr("Device authentication is unavailable. Check biometrics and your device passcode in system settings, or unlock manually without saving."))
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New(tr("Identity verification timed out. Try again."))
	}
	return errors.New(tr("Identity verification incomplete. Unlock manually or retry the saved password."))
}

// Called off the UI thread. Fresh device authentication gates every saved
// password use and every opt-in save. Only a confirmed successful unlock is
// saved. Cancellation never submits the password or creates a new saved item.
func (m *Client) DesktopPerformUnlock(ctx context.Context, client *control.Client, store phonecache.SecretStore, key, password string, saved, remember bool) (result desktopUnlockResult) {
	defer func() { password = "" }()
	if saved || remember {
		if store == nil || key == "" {
			result.Err = errors.New(tr("Keychain unavailable. Disable password saving and unlock manually."))
			return
		}
		authCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err := m.authenticateDesktopUnlock(authCtx)
		cancel()
		if err != nil {
			result.Err = desktopUnlockAuthenticationError(err)
			return
		}
	}
	if err := ctx.Err(); err != nil {
		result.Err = err
		return
	}
	if saved {
		data, err := store.Get(key)
		if err != nil || !control.ValidUnlockPassword(string(data)) {
			clear(data)
			result.Err = errors.New(tr("Unable to read the saved password. Enter it manually."))
			return
		}
		password = string(data)
		clear(data)
	}
	requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result.Err = client.Unlock(requestCtx, password)
	if result.Err == nil && remember && !saved {
		// Serialize writes with forgetting a password and removing a pairing.
		m.storeMu.Lock()
		if ctx.Err() == nil {
			data := []byte(password)
			err := store.Set(key, data)
			clear(data)
			result.SavedChanged, result.Saved, result.StorageError = err == nil, err == nil, err != nil
		}
		m.storeMu.Unlock()
	}
	if saved && control.Status(result.Err) == 401 {
		m.storeMu.Lock()
		err := store.Delete(key)
		m.storeMu.Unlock()
		result.SavedChanged, result.StorageError = err == nil, err != nil
	}
	return
}

func (m *Client) DesktopForgetUnlock(p *MobileDesktopPage) {
	if p.Unlock.Busy || m.Store == nil || p.Unlock.Key == "" {
		return
	}
	p.Unlock.Busy = true
	p.Unlock.revision++
	store, key := m.Store, p.Unlock.Key
	m.desktopLeaseDo(p, func(ctx context.Context) func() {
		m.storeMu.Lock()
		err := store.Delete(key)
		m.storeMu.Unlock()
		return func() {
			p.Unlock.Busy = false
			if err != nil {
				p.Unlock.Status = tr("Unable to delete the saved password. Try again.")
				return
			}
			p.Unlock.Saved, p.Unlock.Remember, p.Unlock.Text = false, false, ""
			p.Unlock.Status = tr("Saved login password removed for this Mac")
		}
	})
}
