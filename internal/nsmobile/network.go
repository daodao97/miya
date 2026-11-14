package nsmobile

import (
	"context"

	"miya/internal/apns"
	"miya/internal/notification"
	phonecache "miya/internal/phone/cache"

	"github.com/nsgo-dev/nsgo/core"
	"tailscale.com/net/netmon"
)

// PlatformHooks supplies native services an embedding mobile host can provide
// before NSGo gains implementations. Install before MobileEntry. NSGo's
// ReadNetworkPath/ObserveNetworkPath report ErrPlatformUnsupported on phones;
// the transport still reconnects on timeout and foreground activation.
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
	// NetworkInterfaces supplies Android's native interface snapshot. Android
	// app UIDs cannot enumerate interfaces using NETLINK_ROUTE.
	NetworkInterfaces func() ([]netmon.Interface, error)
	// SecretStore supplies a mobile Keychain/Keystore adapter when available.
	SecretStore func(*core.Scope) phonecache.SecretStore
	// PrepareNetwork may warm up iOS local-network access and return a
	// *remote.PrepareError for a denied/offline path. Nil lets Go's transport
	// trigger the system network permission normally. It must honor ctx.
	PrepareNetwork func(ctx context.Context, relayURL string) error
}

func installNetworkHooks() {
	if PlatformHooks.NetworkInterfaces != nil {
		netmon.RegisterInterfaceGetter(PlatformHooks.NetworkInterfaces)
	}
}
