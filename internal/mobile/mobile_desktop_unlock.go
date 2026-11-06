package mobile

import (
	"context"
	"errors"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"miya/internal/desktop/control"
	"miya/internal/phonecache"
	"miya/internal/widgets"
)

// Only the Keychain holds a saved password. The page retains flags and its
// desktop-specific key; an automatic attempt runs once per observed lock cycle.
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

func (p *MobileDesktopPage) canUnlock() bool { return !p.WindowOnly || p.Pickable }

func (m *App) DesktopCheckSavedUnlock(p *MobileDesktopPage) {
	if !p.canUnlock() || p.Unlock.checked || p.Unlock.Key == "" || m.Store == nil {
		return
	}
	p.Unlock.checked = true
	store, key, revision := m.Store, p.Unlock.Key, p.Unlock.revision
	m.desktopDo(p, func(ctx context.Context) func() {
		data, err := store.Get(key)
		saved := err == nil && len(data) > 0
		clear(data)
		return func() {
			if p.Unlock.revision != revision {
				return
			}
			p.Unlock.Saved = saved
			if err != nil && !errors.Is(err, mygo.ErrSecretNotFound) && !p.Unlock.Busy {
				p.Unlock.Status = tr("Unable to read the saved password. Unlock manually.")
			}
			m.desktopAutoUnlock(p)
		}
	})
}

func (m *App) DesktopLockChanged(p *MobileDesktopPage, locked bool) {
	if !p.canUnlock() || p.Unlock.Busy {
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
	p.Queue, p.text, p.Image = nil, "", nil
	m.desktopAutoUnlock(p)
}

func (m *App) desktopAutoUnlock(p *MobileDesktopPage) {
	if p.Active && p.canUnlock() && p.Unlock.Locked && p.Unlock.Saved && !p.Unlock.attempted && !p.Unlock.Busy {
		m.desktopBeginUnlock(p, true)
	}
}

func (m *App) DesktopUnlock(p *MobileDesktopPage) {
	m.desktopBeginUnlock(p, false)
}

func (m *App) desktopBeginUnlock(p *MobileDesktopPage, saved bool) {
	if !p.Active || !p.canUnlock() || !p.Unlock.Locked || p.Unlock.Busy || (!saved && !control.ValidUnlockPassword(p.Unlock.Text)) {
		return
	}
	password, remember := p.Unlock.Text, p.Unlock.Remember
	store, key := m.Store, p.Unlock.Key
	p.Unlock.Text, p.Unlock.Busy, p.Unlock.attempted = "", true, true
	p.Unlock.Status = tr("Unlocking Mac…")
	if saved || remember {
		p.Unlock.Status = tr("Verify your identity to use your Mac login password…")
	}
	m.desktopDo(p, func(ctx context.Context) func() {
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

func (m *App) authenticateDesktopUnlock(ctx context.Context) error {
	if m.DesktopAuthenticate != nil {
		return m.DesktopAuthenticate(ctx)
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
func (m *App) DesktopPerformUnlock(ctx context.Context, client *control.Client, store phonecache.SecretStore, key, password string, saved, remember bool) (result desktopUnlockResult) {
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
		m.DesktopWrites.Mu.Lock()
		if ctx.Err() == nil {
			data := []byte(password)
			err := store.Set(key, data)
			clear(data)
			result.SavedChanged, result.Saved, result.StorageError = err == nil, err == nil, err != nil
		}
		m.DesktopWrites.Mu.Unlock()
	}
	if saved && control.Status(result.Err) == 401 {
		m.DesktopWrites.Mu.Lock()
		err := store.Delete(key)
		m.DesktopWrites.Mu.Unlock()
		result.SavedChanged, result.StorageError = err == nil, err != nil
	}
	return
}

func (m *App) DesktopForgetUnlock(p *MobileDesktopPage) {
	if p.Unlock.Busy || m.Store == nil || p.Unlock.Key == "" {
		return
	}
	p.Unlock.Busy = true
	p.Unlock.revision++
	store, key := m.Store, p.Unlock.Key
	m.desktopDo(p, func(ctx context.Context) func() {
		m.DesktopWrites.Mu.Lock()
		err := store.Delete(key)
		m.DesktopWrites.Mu.Unlock()
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

func (m *App) desktopUnlockView(c *ui.Context, p *MobileDesktopPage) {
	if !p.canUnlock() || (!p.Unlock.Locked && p.Unlock.Status == "") {
		return
	}
	pal := m.Pal
	// Unlocked, only a short status line remains; the saved password lives
	// in the toolbar so it never takes a row of its own.
	if !p.Unlock.Locked {
		ui.Text(c.Key("desktop-unlock-status"), p.Unlock.Status).FontSize(m.Font(12)).TextColor(pal.muted).MaxLines(2)
		return
	}
	ui.Column(c.Key("desktop-unlock")).FillWidth().Padding(12).Gap(10).Background(pal.surface).Radius(16).Children(func() {
		if p.Unlock.Locked {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				widgets.Icon(c, "lock", 18, pal.ink)
				ui.Text(c, tr("Mac is locked")).FontSize(m.Font(15)).FontWeight(600)
			})
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				field := ui.TextInput(c.Key("desktop-unlock-text"), &p.Unlock.Text).Grow(1).MinWidth(0).MinHeight(44).Padding(10, 16).Background(pal.Bg).Radius(22).
					InputOptions(ui.InputOptions{Correction: ui.CorrectionOff, Capitalization: ui.CapitalizeNone, Return: ui.ReturnGo})
				field.Password().Label(tr("Mac login password")).Placeholder(tr("Enter Mac login password")).Disabled(p.Unlock.Busy)
				if m.pill(c, "desktop-unlock", tr("Unlock"), true).Disabled(p.Unlock.Busy || !control.ValidUnlockPassword(p.Unlock.Text)).Clicked() {
					m.DesktopUnlock(p)
				}
			})
			if p.Unlock.Saved {
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					if m.ApprovalButton(c, "desktop-unlock-saved", tr("Verify and unlock"), false, p.Unlock.Busy).Grow(1).MinWidth(0).Clicked() {
						m.desktopBeginUnlock(p, true)
					}
					forget := m.phoneButton(c.Key("desktop-unlock-forget")).PaddingX(10).Justify(ui.Center).Label(tr("Forget password")).Disabled(p.Unlock.Busy)
					forget.Children(func() {
						ui.Text(c, tr("Forget password")).FontSize(m.Font(14)).FontWeight(500).TextColor(pal.link)
					})
					if forget.Clicked() {
						m.DesktopForgetUnlock(p)
					}
				})
			}
			if m.Store != nil && p.Unlock.Key != "" {
				ui.Checkbox(c.Key("desktop-unlock-save"), &p.Unlock.Remember, tr("Save password for automatic unlock after identity verification")).Disabled(p.Unlock.Busy)
			}
		}
		if p.Unlock.Status != "" {
			ui.Text(c, p.Unlock.Status).FontSize(m.Font(12)).TextColor(pal.muted).MaxLines(3)
		}
	})
}
