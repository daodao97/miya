package remote

import (
	"context"
	"errors"
	"runtime"
	"time"
)

// PrepareNetwork, when set, brings up the app's network access through the
// system's own stack before the tunnel opens raw sockets: on iOS one request
// through Foundation raises the cellular-data and local-network prompts and
// the VPN and DNS64 routes. Desktops and servers leave it nil. The phone
// sets it; failures that have a kind come back as *PrepareError.
var PrepareNetwork func(ctx context.Context, rawURL string) error

// PrepareError reports why PrepareNetwork failed: Kind is "offline",
// "timeout", "canceled" or "failed".
type PrepareError struct {
	Kind string
	Err  error
}

func (e *PrepareError) Error() string { return e.Err.Error() }
func (e *PrepareError) Unwrap() error { return e.Err }

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
	// opens raw sockets; nothing is prepared on desktops.
	if PrepareNetwork != nil {
		if err := PrepareNetwork(ctx, RelayURL(raw)); err != nil {
			return nil, networkError(err)
		}
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
	var failure *PrepareError
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
