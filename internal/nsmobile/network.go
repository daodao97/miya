package nsmobile

import (
	"context"

	"miya/internal/apns"
	"miya/internal/notification"
)

// PlatformHooks supplies services that NSGo does not yet expose. Install
// before MobileEntry. Credentials, QR scanning and paths use NSGo directly.
var PlatformHooks struct {
	// RegisterPush asks APNs for an alert device token, in hex, honoring ctx.
	// NSGo currently has no general remote-notification registration API.
	RegisterPush func(context.Context) (string, error)
	// ObserveRemoteNotifications delivers queued launch taps and foreground
	// payloads decoded using internal/apns.Payload's ID/title/body/data format.
	// The host suppresses its own foreground banner; the adapter decides whether
	// to show one through Scope.ShowNotification. The returned function unhooks.
	ObserveRemoteNotifications func(func(notification.LiveMessage, bool)) func()
	// KeepRunning acquires an Android foreground-service lease while on screen.
	// A nil release means acquisition failed; background then disconnects normally.
	KeepRunning func(string) func()
	// RegisterActivity forwards ActivityKit token rotations to a desktop
	// apns.ActivityManager. An empty DeviceToken unregisters the subscription.
	// No provider signing keys belong in the phone. There is no default protocol
	// operation for this yet. The host must serialize registrations/unregisters.
	RegisterActivity func(apns.ActivityRegistration)
	// DesktopAuthenticate must perform fresh biometric/device authentication
	// before saving or using a desktop password. NSGo has no equivalent API.
	DesktopAuthenticate func(context.Context) error
}
