//go:build windows || ios

package main

import (
	"context"
	"miya/internal/remote"
)

func (c *chatController) serveRemoteTerminal(ctx context.Context, wire *remote.Conn) {
	_ = wire.Send(terminalFrame{Op: "ready", Error: "Server terminal is not supported on this platform"})
}
