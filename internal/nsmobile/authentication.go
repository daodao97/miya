package nsmobile

import (
	"context"

	"github.com/nsgo-dev/nsgo/core"

	phoneclient "miya/internal/phone/client"
)

type desktopAuthentication struct {
	known, available, override bool
	kind                       core.BiometricKind
	revision                   uint64
}

func (m *App) setupDesktopAuthentication() {
	if m.DesktopAuthenticate == nil {
		m.DesktopAuthenticate = PlatformHooks.DesktopAuthenticate
	}
	if m.DesktopAuthenticate != nil {
		m.authentication = desktopAuthentication{known: true, available: true, override: true, kind: core.BiometricNone}
		return
	}
	m.DesktopAuthenticate = m.authenticateDesktop
	m.refreshDesktopAuthentication()
}

// Availability is presentation state, never authorization. Query again before
// every prompt, and refresh the UI after device settings or enrollment changes.
func (m *App) refreshDesktopAuthentication() {
	if m.authentication.override {
		return
	}
	m.authentication.revision++
	revision := m.authentication.revision
	m.cx.BiometricAvailability(func(kind core.BiometricKind) {
		if revision == m.authentication.revision {
			m.setDesktopAuthentication(kind != core.BiometricNone, kind)
		}
	})
}

func (m *App) setDesktopAuthentication(available bool, kind core.BiometricKind) {
	m.authentication.known, m.authentication.available, m.authentication.kind = true, available, kind
	if !available && m.DesktopPage != nil {
		// A hidden save choice must not prevent subsequent manual unlock.
		m.DesktopPage.Unlock.Remember = false
	}
	m.change()
}

// Called by the phone worker. NSGo services must run on the engine; a buffered
// reply lets late native callbacks finish after the page or timeout cancels.
// Only an explicit successful result authorizes reading/submitting a password.
func (m *App) authenticateDesktop(ctx context.Context) error {
	done := make(chan error, 1)
	m.cx.Post(func() {
		if ctx.Err() != nil {
			return
		}
		m.cx.BiometricAvailability(func(kind core.BiometricKind) {
			m.authentication.revision++
			m.setDesktopAuthentication(kind != core.BiometricNone, kind)
			if kind == core.BiometricNone {
				done <- phoneclient.ErrDesktopAuthenticationUnavailable
				return
			}
			if ctx.Err() != nil {
				return
			}
			m.cx.AuthenticateBiometric(tr("Verify your identity to save or use your Mac login password"), func(result core.BiometricResult) {
				if result == core.BiometricSuccess {
					done <- ctx.Err()
				} else {
					done <- context.Canceled
				}
			})
		})
	})
	select {
	case err := <-done:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *App) desktopAuthenticationLabel() string {
	if !m.authentication.known {
		return tr("Checking…")
	}
	if !m.authentication.available {
		return tr("Password saving requires device authentication, which is unavailable in this NSGo build.")
	}
	switch m.authentication.kind {
	case core.BiometricFace:
		if m.cx.Env().OS() == core.OSIOS {
			return tr("Face ID")
		}
		return tr("Face recognition or device screen lock")
	case core.BiometricFingerprint:
		if m.cx.Env().OS() == core.OSIOS || m.cx.Env().OS() == core.OSMacOS {
			return tr("Touch ID")
		}
		return tr("Fingerprint or device screen lock")
	case core.BiometricNone:
		return tr("Device screen lock")
	default:
		return tr("Biometrics or device screen lock")
	}
}

func (m *App) desktopAuthenticationStatus() core.Node {
	return column(text(tr("Device authentication")), core.Text{TestID: "desktop-authentication-status", ContentFunc: live(m, m.desktopAuthenticationLabel)})
}
