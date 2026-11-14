package nsmobile

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"tailscale.com/net/netmon"
)

// phoneNetwork owns the native snapshot. Tailcat asks for interfaces on its
// workers, while NSGo delivers paths on the engine goroutine.
type phoneNetwork struct {
	cx        *core.Scope
	mu        sync.Mutex
	path      core.NetworkPath
	addresses []net.Addr
	revision  uint64
	warmup    func(context.Context, string) error
}

func (m *App) setupNetwork(cx *core.Scope) {
	n := &phoneNetwork{cx: cx, warmup: prepareSystemNetwork}
	if runtime.GOOS == "android" {
		// Register before starting any connection. Never query NETLINK_ROUTE in
		// an Android app UID, including when the native path has no interfaces.
		netmon.RegisterInterfaceGetter(n.interfaces)
	}
	if m.PrepareNetwork == nil {
		m.PrepareNetwork = n.prepare
	}
	stop := cx.ObserveNetworkPathDetails(func(path core.NetworkPath, err error) {
		if err == nil {
			n.update(path)
			m.NetworkPathChanged(path.Available, pathIdentity(path))
			m.change()
		}
	})
	cx.Cleanup(stop)
}

// Native order is not meaningful. Include name and kind so even an Android
// transport without an interface name can wake a waiting reconnect.
func pathIdentity(path core.NetworkPath) string {
	parts := make([]string, 0, len(path.Interfaces))
	for _, iface := range path.Interfaces {
		parts = append(parts, fmt.Sprintf("%q:%q", iface.Name, iface.Kind))
	}
	slices.Sort(parts)
	return strings.Join(slices.Compact(parts), ",")
}

func (n *phoneNetwork) update(path core.NetworkPath) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.updateLocked(path)
}

func (n *phoneNetwork) updateLocked(path core.NetworkPath) {
	if path.Available != n.path.Available || pathIdentity(path) != pathIdentity(n.path) {
		n.addresses = nil
	}
	n.revision++
	n.path = path
	n.path.Interfaces = slices.Clone(path.Interfaces)
}

func (n *phoneNetwork) interfaces() ([]netmon.Interface, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []netmon.Interface
	seen := map[string]bool{}
	for _, v := range n.path.Interfaces {
		if v.Name == "" || seen[v.Name] {
			continue
		}
		seen[v.Name] = true
		flags := net.Flags(0)
		if n.path.Available {
			flags = net.FlagUp | net.FlagRunning
		}
		if v.Kind == core.NetworkLoopback {
			flags |= net.FlagLoopback
		}
		iface := netmon.Interface{
			Interface: &net.Interface{Name: v.Name, Flags: flags},
			// NSGo exposes names/transports, not IP addresses, indices or MTUs.
			// A nonnil empty list is authoritative; nil falls back to netlink.
			AltAddrs: []net.Addr{},
		}
		fillInterfaceDetails(&iface)
		if len(out) == 0 {
			for _, addr := range n.addresses {
				ip := addr.(*net.IPNet)
				// ioctl already supplies the IPv4 subnet where available.
				if ip.IP.To4() == nil || len(iface.AltAddrs) == 0 {
					iface.AltAddrs = append(iface.AltAddrs, &net.IPNet{IP: slices.Clone(ip.IP), Mask: slices.Clone(ip.Mask)})
				}
			}
		}
		out = append(out, iface)
	}
	return out, nil
}

// Called on the connection worker before opening Tailcat. Read a fresh path
// rather than waiting for an observer update; cancellation also covers a late
// native reply. Availability is only a preflight, never proof of relay access.
func (n *phoneNetwork) prepare(ctx context.Context, relay string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	type result struct {
		path core.NetworkPath
		err  error
	}
	done := make(chan result, 1)
	n.cx.Post(func() {
		if ctx.Err() != nil {
			return
		}
		n.mu.Lock()
		revision := n.revision
		n.mu.Unlock()
		n.cx.ReadNetworkPathDetails(func(p core.NetworkPath, err error) {
			if err == nil {
				n.mu.Lock()
				if revision == n.revision {
					n.updateLocked(p)
				} else {
					// A newer observation wins over an in-flight initial read.
					p = n.path
					p.Interfaces = slices.Clone(p.Interfaces)
				}
				n.mu.Unlock()
			}
			done <- result{p, err}
		})
	})
	var path core.NetworkPath
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r := <-done:
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.err != nil && !errors.Is(r.err, core.ErrPlatformUnsupported) {
			return r.err
		}
		if r.err == nil {
			path = r.path
			if !r.path.Available {
				return errors.New(tr("Network unavailable"))
			}
		}
	}
	if err := n.warmup(ctx, relay); err != nil {
		return err
	}
	addresses := routeInterfaceAddresses(ctx, relay)
	if err := ctx.Err(); err != nil {
		return err
	}
	n.mu.Lock()
	if path.Available == n.path.Available && pathIdentity(path) == pathIdentity(n.path) {
		n.addresses = addresses
	}
	n.mu.Unlock()
	return nil
}
