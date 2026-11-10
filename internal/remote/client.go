package remote

import (
	"context"
	"errors"
	"runtime"
	"time"

	"github.com/egoist/mygo/plugins/network"
)

// Connect opens a tunnel to the desktop in raw, a miya:// link or bare
// Tailcat address. Closing the Conn closes its tunnel.
func Connect(ctx context.Context, raw string) (*Conn, error) {
	return connectPort(ctx, raw, Port)
}

// ConnectService opens the full desktop protocol on a server installation.
func ConnectService(ctx context.Context, raw string) (*Conn, error) {
	return connectPort(ctx, raw, ServicePort)
}

func connectPort(ctx context.Context, raw string, port uint16) (*Conn, error) {
	addr, err := ParseLink(raw)
	if err != nil {
		return nil, err
	}
	// iOS establishes the app's network access through Foundation before Go
	// opens raw sockets; MyGo returns at once on desktops.
	if err := network.Prepare(ctx, RelayURL(raw)); err != nil {
		return nil, networkError(err)
	}
	server := addr
	if runtime.GOOS == "ios" {
		server = hostnameRelayAddr(addr)
	}
	t := openTunnel(addr, server)
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	nc, err := t.client.DialTCPPort(dctx, port)
	if err != nil {
		t.close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("Unable to connect to Miya. Check that the target is online and remote connections are enabled.")
	}
	t.used.Store(true)
	conn := newConn(nc, t.close)
	conn.dialPort = t.client.DialTCPPort
	return conn, nil
}

func networkError(err error) error {
	var failure *network.Error
	if !errors.As(err, &failure) {
		return err
	}
	switch failure.Kind {
	case "offline":
		return errors.New("Network unavailable")
	case "timeout":
		return errors.New("Relay connection timed out")
	case "canceled":
		return context.Canceled
	}
	return errors.New("Unable to reach the relay server")
}
