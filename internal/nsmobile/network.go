package nsmobile

import (
	"context"

	phonecache "miya/internal/phone/cache"

	"github.com/nsgo-dev/nsgo/core"
	"tailscale.com/net/netmon"
)

// PlatformHooks supplies native services an embedding mobile host can provide
// before NSGo gains implementations. Install before MobileEntry. NSGo's
// ReadNetworkPath/ObserveNetworkPath report ErrPlatformUnsupported on phones;
// the transport still reconnects on timeout and foreground activation.
var PlatformHooks struct {
	DesktopAuthenticate func(context.Context) error
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
