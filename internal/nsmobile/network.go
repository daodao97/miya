package nsmobile

import (
	"context"

	"miya/internal/apns"
	"miya/internal/notification"
)

// PlatformHooks supplies optional host overrides. Install before MobileEntry.
// Notification, activity and foreground-service defaults use NSGo directly.
var PlatformHooks struct {
	// RegisterPush asks APNs for an alert device token, in hex, honoring ctx.
	// Nil uses mobile.RegisterRemoteNotifications and its token events.
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
	// Nil uses the authenticated activity protocol operation. Provider signing
	// keys remain on the desktop. Overrides must serialize registrations.
	RegisterActivity func(apns.ActivityRegistration)
	// DesktopAuthenticate overrides NSGo's device owner authentication (with
	// passcode fallback). It must freshly authenticate before each save/use.
	DesktopAuthenticate func(context.Context) error
}
