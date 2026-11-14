package nsmobile

import (
	"context"
	"net"
	"net/url"
	"time"

	"golang.org/x/sys/unix"
	"tailscale.com/net/netmon"
)

// NSGo supplies the interface names without NETLINK_ROUTE. Query each known
// name with socket ioctls for Tailcat's IPv4 address, index and MTU. Android
// denies Go's net.Interfaces/net.Interface.Addrs netlink enumeration to app UIDs.
func fillInterfaceDetails(iface *netmon.Interface) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return
	}
	defer unix.Close(fd)
	req, err := unix.NewIfreq(iface.Name)
	if err != nil {
		return
	}
	if unix.IoctlIfreq(fd, unix.SIOCGIFINDEX, req) == nil {
		iface.Index = int(req.Uint32())
	}
	if unix.IoctlIfreq(fd, unix.SIOCGIFMTU, req) == nil {
		iface.MTU = int(req.Uint32())
	}
	if unix.IoctlIfreq(fd, unix.SIOCGIFADDR, req) == nil {
		if addr, err := req.Inet4Addr(); err == nil {
			ip := net.IPv4(addr[0], addr[1], addr[2], addr[3])
			mask := net.CIDRMask(32, 32)
			if unix.IoctlIfreq(fd, unix.SIOCGIFNETMASK, req) == nil {
				if bits, err := req.Inet4Addr(); err == nil {
					mask = net.IPv4Mask(bits[0], bits[1], bits[2], bits[3])
				}
			}
			iface.AltAddrs = append(iface.AltAddrs, &net.IPNet{IP: ip, Mask: mask})
		}
	}
}

// IPv6 has no SIOCGIFADDR enumeration. Connecting a UDP socket chooses a
// real source address for the active relay route without transmitting a
// packet. This also covers IPv4 on devices refusing address ioctls. Keep
// discovery bounded; Tailcat still owns the actual reachability decision.
func routeInterfaceAddresses(ctx context.Context, relay string) []net.Addr {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	u, err := url.Parse(relay)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if err != nil {
		return nil
	}
	var out []net.Addr
	seen := map[string]bool{}
	for _, ip := range ips {
		family, bits := "udp6", 128
		if ip.IP.To4() != nil {
			family, bits = "udp4", 32
		}
		if seen[family] {
			continue
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, family, net.JoinHostPort(ip.String(), "443"))
		if err != nil {
			continue
		}
		local := conn.LocalAddr().(*net.UDPAddr)
		_ = conn.Close()
		if local.IP.IsUnspecified() || local.IP.IsLoopback() {
			continue
		}
		out = append(out, &net.IPNet{IP: append(net.IP(nil), local.IP...), Mask: net.CIDRMask(bits, bits)})
		seen[family] = true
	}
	return out
}
