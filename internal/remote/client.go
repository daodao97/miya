package remote

import (
	"context"
	"errors"
	"runtime"
	"time"

	"github.com/egoist/mygo"
)

// Connect opens a tunnel to the desktop in raw, a miya:// link or bare
// Tailcat address. Closing the Conn closes its tunnel.
func Connect(ctx context.Context, raw string) (*Conn, error) {
	addr, err := ParseLink(raw)
	if err != nil {
		return nil, err
	}
	// iOS establishes the app's network access through Foundation before Go
	// opens raw sockets; MyGo returns at once on desktops.
	if err := mygo.Network.Prepare(ctx, RelayURL(raw)); err != nil {
		return nil, networkError(err)
	}
	server := addr
	if runtime.GOOS == "ios" {
		server = hostnameRelayAddr(addr)
	}
	t := openTunnel(addr, server)
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	nc, err := t.client.DialTCPPort(dctx, Port)
	if err != nil {
		t.close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("无法连接桌面端，请确认 Miya 桌面端已开启手机连接")
	}
	t.used.Store(true)
	conn := newConn(nc, t.close)
	conn.dialPort = t.client.DialTCPPort
	return conn, nil
}

func networkError(err error) error {
	var failure *mygo.NetworkError
	if !errors.As(err, &failure) {
		return err
	}
	switch failure.Kind {
	case "offline":
		return errors.New("网络不可用")
	case "timeout":
		return errors.New("连接中继超时")
	case "canceled":
		return context.Canceled
	}
	return errors.New("无法访问中继服务器")
}
