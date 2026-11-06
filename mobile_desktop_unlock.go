package main

import (
	"context"
	"errors"
	"time"

	"miya/internal/desktop/control"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Only the Keychain holds a saved password. The page retains flags and its
// desktop-specific key; an automatic attempt runs once per observed lock cycle.
type desktopUnlockState struct {
	revision                                          uint64
	locked, busy, saved, checked, attempted, remember bool
	key, text, status                                 string
}

func desktopUnlockKey(id string) string {
	if id == "" {
		return ""
	}
	return "desktop-unlock-v1-" + id
}

func (p *mobileDesktopPage) canUnlock() bool { return !p.windowOnly || p.pickable }

func (m *mobileApp) desktopCheckSavedUnlock(p *mobileDesktopPage) {
	if !p.canUnlock() || p.unlock.checked || p.unlock.key == "" || m.store == nil {
		return
	}
	p.unlock.checked = true
	store, key, revision := m.store, p.unlock.key, p.unlock.revision
	m.desktopDo(p, func(ctx context.Context) func() {
		data, err := store.Get(key)
		saved := err == nil && len(data) > 0
		clear(data)
		return func() {
			if p.unlock.revision != revision {
				return
			}
			p.unlock.saved = saved
			if err != nil && !errors.Is(err, mygo.ErrSecretNotFound) && !p.unlock.busy {
				p.unlock.status = tr("Unable to read the saved password. Unlock manually.")
			}
			m.desktopAutoUnlock(p)
		}
	})
}

func (m *mobileApp) desktopLockChanged(p *mobileDesktopPage, locked bool) {
	if !p.canUnlock() || p.unlock.busy {
		return
	}
	wasLocked := p.unlock.locked
	p.unlock.locked = locked
	if !locked {
		if wasLocked {
			p.unlock.status = ""
		}
		p.unlock.text, p.unlock.attempted = "", false
		return
	}
	// Old queued clicks and text must not interleave with unlocking.
	p.queue, p.text, p.image = nil, "", nil
	m.desktopAutoUnlock(p)
}

func (m *mobileApp) desktopAutoUnlock(p *mobileDesktopPage) {
	if p.active && p.canUnlock() && p.unlock.locked && p.unlock.saved && !p.unlock.attempted && !p.unlock.busy {
		m.desktopBeginUnlock(p, true)
	}
}

func (m *mobileApp) desktopUnlock(p *mobileDesktopPage) {
	m.desktopBeginUnlock(p, false)
}

func (m *mobileApp) desktopBeginUnlock(p *mobileDesktopPage, saved bool) {
	if !p.active || !p.canUnlock() || !p.unlock.locked || p.unlock.busy || (!saved && !control.ValidUnlockPassword(p.unlock.text)) {
		return
	}
	password, remember := p.unlock.text, p.unlock.remember
	store, key := m.store, p.unlock.key
	p.unlock.text, p.unlock.busy, p.unlock.attempted = "", true, true
	p.unlock.status = tr("Unlocking Mac…")
	if saved || remember {
		p.unlock.status = tr("Verify your identity to use your Mac login password…")
	}
	m.desktopDo(p, func(ctx context.Context) func() {
		result := m.desktopPerformUnlock(ctx, p.client, store, key, password, saved, remember)
		password = ""
		return func() {
			p.unlock.busy = false
			if result.savedChanged {
				p.unlock.revision++
				p.unlock.saved = result.saved
			}
			switch status := control.Status(result.err); {
			case result.err == nil, status == 409:
				p.unlock.locked, p.unlock.status = false, ""
			case status == 410:
				m.desktopFinish(p, result.err.Error())
			case status == 401:
				p.unlock.status = tr("The password did not unlock your Mac. Check it and retry manually.")
			default:
				p.unlock.status = result.err.Error()
			}
			if result.storageError {
				p.unlock.status = tr("Keychain operation failed. Password storage is unchanged.")
			}
		}
	})
}

type desktopUnlockResult struct {
	err                               error
	savedChanged, saved, storageError bool
}

func (m *mobileApp) authenticateDesktopUnlock(ctx context.Context) error {
	if m.desktopAuthenticate != nil {
		return m.desktopAuthenticate(ctx)
	}
	return mygo.Biometrics.Authenticate(ctx, mygo.AuthenticationOptions{
		Reason: tr("Verify your identity to save or use your Mac login password"), AllowDevicePasscode: true,
	})
}

// Keep native failure details out of the UI, but distinguish packaging errors,
// device setup, and cancellation so the person can take the right next step.
func desktopUnlockAuthenticationError(err error) error {
	message := "Identity verification incomplete. Unlock manually or retry the saved password."
	var auth *mygo.AuthenticationError
	switch {
	case errors.Is(err, mygo.ErrUnsupported):
		message = "Identity verification is unavailable in this app build. Update Miya, or disable password saving to unlock manually."
	case errors.Is(err, context.DeadlineExceeded):
		message = "Identity verification timed out. Try again."
	case errors.As(err, &auth):
		switch auth.Code {
		case mygo.AuthenticationMissingPurpose:
			message = "Identity verification is unavailable in this app build. Update Miya, or disable password saving to unlock manually."
		case mygo.AuthenticationPasscodeNotSet:
			message = "Set a device passcode in iPhone Settings to save or use your Mac password."
		case mygo.AuthenticationInactive, mygo.AuthenticationBusy:
			message = "Return to Miya and close any system dialog, then retry identity verification."
		case mygo.AuthenticationFailed:
			message = "Identity verification failed. Try Face ID or your device passcode again."
		case mygo.AuthenticationUnavailable, mygo.AuthenticationNotEnrolled, mygo.AuthenticationLockedOut:
			message = "Device authentication is unavailable. Check Face ID and passcode in iPhone Settings, or unlock manually without saving."
		}
	}
	return errors.New(tr(message))
}

// Called off the UI thread. Fresh device authentication gates every saved
// password use and every opt-in save. Only a confirmed successful unlock is
// saved. Cancellation never submits the password or creates a new saved item.
func (m *mobileApp) desktopPerformUnlock(ctx context.Context, client *control.Client, store mobileSecretStore, key, password string, saved, remember bool) (result desktopUnlockResult) {
	defer func() { password = "" }()
	if saved || remember {
		if store == nil || key == "" {
			result.err = errors.New(tr("Keychain unavailable. Disable password saving and unlock manually."))
			return
		}
		authCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err := m.authenticateDesktopUnlock(authCtx)
		cancel()
		if err != nil {
			result.err = desktopUnlockAuthenticationError(err)
			return
		}
	}
	if err := ctx.Err(); err != nil {
		result.err = err
		return
	}
	if saved {
		data, err := store.Get(key)
		if err != nil || !control.ValidUnlockPassword(string(data)) {
			clear(data)
			result.err = errors.New(tr("Unable to read the saved password. Enter it manually."))
			return
		}
		password = string(data)
		clear(data)
	}
	requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result.err = client.Unlock(requestCtx, password)
	if result.err == nil && remember && !saved {
		// Serialize writes with forgetting a password and removing a pairing.
		m.desktopWrites.mu.Lock()
		if ctx.Err() == nil {
			data := []byte(password)
			err := store.Set(key, data)
			clear(data)
			result.savedChanged, result.saved, result.storageError = err == nil, err == nil, err != nil
		}
		m.desktopWrites.mu.Unlock()
	}
	if saved && control.Status(result.err) == 401 {
		m.desktopWrites.mu.Lock()
		err := store.Delete(key)
		m.desktopWrites.mu.Unlock()
		result.savedChanged, result.storageError = err == nil, err != nil
	}
	return
}

func (m *mobileApp) desktopForgetUnlock(p *mobileDesktopPage) {
	if p.unlock.busy || m.store == nil || p.unlock.key == "" {
		return
	}
	p.unlock.busy = true
	p.unlock.revision++
	store, key := m.store, p.unlock.key
	m.desktopDo(p, func(ctx context.Context) func() {
		m.desktopWrites.mu.Lock()
		err := store.Delete(key)
		m.desktopWrites.mu.Unlock()
		return func() {
			p.unlock.busy = false
			if err != nil {
				p.unlock.status = tr("Unable to delete the saved password. Try again.")
				return
			}
			p.unlock.saved, p.unlock.remember, p.unlock.text = false, false, ""
			p.unlock.status = tr("Saved login password removed for this Mac")
		}
	})
}

func (m *mobileApp) desktopUnlockView(c *ui.Context, p *mobileDesktopPage) {
	if !p.canUnlock() || (!p.unlock.locked && p.unlock.status == "") {
		return
	}
	pal := m.pal
	// Unlocked, only a short status line remains; the saved password lives
	// in the toolbar so it never takes a row of its own.
	if !p.unlock.locked {
		ui.Text(c.Key("desktop-unlock-status"), p.unlock.status).FontSize(m.font(12)).TextColor(pal.muted).MaxLines(2)
		return
	}
	ui.Column(c.Key("desktop-unlock")).FillWidth().Padding(12).Gap(10).Background(pal.surface).Radius(16).Children(func() {
		if p.unlock.locked {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				icon(c, "lock", 18, pal.ink)
				ui.Text(c, tr("Mac is locked")).FontSize(m.font(15)).FontWeight(600)
			})
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				field := ui.TextInput(c.Key("desktop-unlock-text"), &p.unlock.text).Grow(1).MinWidth(0).MinHeight(44).Padding(10, 16).Background(pal.bg).Radius(22).
					InputOptions(ui.InputOptions{Correction: ui.CorrectionOff, Capitalization: ui.CapitalizeNone, Return: ui.ReturnGo})
				field.Password().Label(tr("Mac login password")).Placeholder(tr("Enter Mac login password")).Disabled(p.unlock.busy)
				if m.pill(c, "desktop-unlock", tr("Unlock"), true).Disabled(p.unlock.busy || !control.ValidUnlockPassword(p.unlock.text)).Clicked() {
					m.desktopUnlock(p)
				}
			})
			if p.unlock.saved {
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					if m.approvalButton(c, "desktop-unlock-saved", tr("Verify and unlock"), false, p.unlock.busy).Grow(1).MinWidth(0).Clicked() {
						m.desktopBeginUnlock(p, true)
					}
					forget := m.phoneButton(c.Key("desktop-unlock-forget")).PaddingX(10).Justify(ui.Center).Label(tr("Forget password")).Disabled(p.unlock.busy)
					forget.Children(func() {
						ui.Text(c, tr("Forget password")).FontSize(m.font(14)).FontWeight(500).TextColor(pal.link)
					})
					if forget.Clicked() {
						m.desktopForgetUnlock(p)
					}
				})
			}
			if m.store != nil && p.unlock.key != "" {
				ui.Checkbox(c.Key("desktop-unlock-save"), &p.unlock.remember, tr("Save password for automatic unlock after identity verification")).Disabled(p.unlock.busy)
			}
		}
		if p.unlock.status != "" {
			ui.Text(c, p.unlock.status).FontSize(m.font(12)).TextColor(pal.muted).MaxLines(3)
		}
	})
}
