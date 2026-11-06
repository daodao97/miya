//go:build windows || ios

package controller

import (
	"context"

	"miya/internal/remote"
	svcproto "miya/internal/service/protocol"
)

func (c *Controller) ServeRemoteTerminal(ctx context.Context, wire *remote.Conn) {
	_ = wire.Send(svcproto.TerminalFrame{Op: "ready", Error: "Server terminal is not supported on this platform"})
}
