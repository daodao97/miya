//go:build windows || ios

package controller

import (
	"context"
	"miya/internal/appmodel"
	"miya/internal/remote"
)

func (c *Controller) ServeRemoteTerminal(ctx context.Context, wire *remote.Conn) {
	_ = wire.Send(appmodel.TerminalFrame{Op: "ready", Error: "Server terminal is not supported on this platform"})
}
