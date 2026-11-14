//go:build !android

package nsmobile

import (
	"context"
	"net"
	"tailscale.com/net/netmon"
)

func fillInterfaceDetails(*netmon.Interface) {}

func routeInterfaceAddresses(context.Context, string) []net.Addr { return nil }
