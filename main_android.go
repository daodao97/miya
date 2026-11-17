//go:build android && cgo

package main

import (
	"context"
	"net"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/network"
	"tailscale.com/net/netmon"
)

// MyGo's Android host runs main itself and sends the log to logcat.
func init() {
	mygo.App.SetName("Miya")
	// Android app UIDs cannot enumerate interfaces through NETLINK_ROUTE:
	// the system lists them through Java (network.Interfaces).
	netmon.RegisterInterfaceGetter(androidNetworkInterfaces)
}

func androidNetworkInterfaces() ([]netmon.Interface, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list, err := network.Interfaces(ctx)
	if err != nil {
		return nil, err
	}
	var out []netmon.Interface
	for _, v := range list {
		flags := net.Flags(0)
		if v.Up {
			flags |= net.FlagUp | net.FlagRunning
		}
		if v.Loopback {
			flags |= net.FlagLoopback
		}
		// A nil AltAddrs tells netmon to fall back to native netlink. Keep
		// even an addressless Java interface authoritative on Android.
		addresses := make([]net.Addr, 0, len(v.Addresses))
		for _, raw := range v.Addresses {
			ip, subnet, err := net.ParseCIDR(raw)
			if err == nil {
				subnet.IP = ip
				addresses = append(addresses, subnet)
			}
		}
		out = append(out, netmon.Interface{
			Interface: &net.Interface{Name: v.Name, Index: v.Index, MTU: v.MTU, Flags: flags},
			AltAddrs:  addresses,
		})
	}
	return out, nil
}
