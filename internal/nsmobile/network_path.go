package nsmobile

import (
	"context"
	"net"
	"runtime"
	"sync"

	"github.com/nsgo-dev/nsgo/core"
	"tailscale.com/net/netmon"
)

// phoneNetwork provides Tailcat with the route address where Android cannot
// enumerate interfaces with netlink under an ordinary app UID.
type phoneNetwork struct {
	mu        sync.Mutex
	addresses []net.Addr
}

func (m *App) setupNetwork(cx *core.Scope) {
	n := &phoneNetwork{}
	if runtime.GOOS == "android" {
		netmon.RegisterInterfaceGetter(n.interfaces)
	}
	if m.PrepareNetwork == nil {
		m.PrepareNetwork = n.prepare
	}
	update := func(path core.Network) {
		n.mu.Lock()
		n.addresses = nil
		n.mu.Unlock()
		kind := "unmetered"
		if path.Expensive {
			kind = "metered"
		}
		m.NetworkPathChanged(path.Online, kind)
		m.change()
	}
	update(cx.Env().Network())
	cx.OnNetworkChange(update)
}

func (n *phoneNetwork) interfaces() ([]netmon.Interface, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.addresses) == 0 {
		return []netmon.Interface{}, nil
	}
	addresses := make([]net.Addr, 0, len(n.addresses))
	for _, address := range n.addresses {
		ip := address.(*net.IPNet)
		addresses = append(addresses, &net.IPNet{IP: append(net.IP(nil), ip.IP...), Mask: append(net.IPMask(nil), ip.Mask...)})
	}
	return []netmon.Interface{{
		Interface: &net.Interface{Name: "miya-route", Flags: net.FlagUp | net.FlagRunning},
		AltAddrs:  addresses,
	}}, nil
}

func (n *phoneNetwork) prepare(ctx context.Context, relay string) error {
	if err := prepareSystemNetwork(ctx, relay); err != nil {
		return err
	}
	if runtime.GOOS == "android" {
		addresses := routeInterfaceAddresses(ctx, relay)
		n.mu.Lock()
		n.addresses = addresses
		n.mu.Unlock()
	}
	return ctx.Err()
}
