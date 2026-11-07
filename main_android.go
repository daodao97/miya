//go:build android && cgo

package main

import (
	"log"
	"net"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/android/jni"
	"tailscale.com/net/netmon"
)

type androidLogWriter struct{}

func (androidLogWriter) Write(p []byte) (int, error) {
	jni.Log(string(p))
	return len(p), nil
}

func init() {
	log.SetOutput(androidLogWriter{})
	// Android app UIDs cannot enumerate interfaces through NETLINK_ROUTE.
	// The host reports Java's interface snapshot before starting the app.
	netmon.RegisterInterfaceGetter(androidNetworkInterfaces)
	jni.Start(func() {
		mygo.App.SetName("Miya")
		main()
	})
}

func androidNetworkInterfaces() ([]netmon.Interface, error) {
	var out []netmon.Interface
	for _, v := range jni.NetworkInterfaces() {
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
