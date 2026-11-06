//go:build !windows && !ios

package controller

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty"

	"miya/internal/async"
	"miya/internal/remote"
	"miya/internal/svcproto"
	"miya/internal/uiruntime"
)

func (c *Controller) ServeRemoteTerminal(ctx context.Context, wire *remote.Conn) {
	_ = wire.SetReadDeadline(time.Now().Add(15 * time.Second))
	var open svcproto.TerminalFrame
	if wire.Receive(&open) != nil || open.Op != "open" {
		return
	}
	_ = wire.SetReadDeadline(time.Time{})
	known, ok := async.OnMain(ctx, c.Dispatch, func() bool {
		if open.Project == "" {
			return false
		}
		known := open.Project == c.Cwd
		for _, p := range c.Projects() {
			known = known || p.Path == open.Project
		}
		return known
	})
	if !ok || !known {
		_ = wire.Send(svcproto.TerminalFrame{Op: "ready", Error: "Project is not in the server's project list"})
		return
	}
	c.RemoteTerminals.Add(1)
	defer func() { c.RemoteTerminals.Add(-1); uiruntime.Invalidate() }()
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-l")
	cmd.Dir = open.Project
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	master, err := pty.StartWithSize(cmd, terminalSize(open))
	if err != nil {
		_ = wire.Send(svcproto.TerminalFrame{Op: "ready", Error: err.Error()})
		return
	}
	done := make(chan struct{})
	defer close(done)
	defer func() {
		master.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP)
		go cmd.Wait()
	}()
	if wire.Send(svcproto.TerminalFrame{Op: "ready"}) != nil {
		return
	}
	go func() {
		select {
		case <-ctx.Done():
			wire.Close()
		case <-done:
		}
	}()
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := master.Read(buf)
			if n > 0 && wire.Send(svcproto.TerminalFrame{Op: "output", Data: buf[:n]}) != nil {
				wire.Close()
				return
			}
			if err != nil {
				_ = wire.Send(svcproto.TerminalFrame{Op: "exit"})
				wire.Close()
				return
			}
		}
	}()
	for {
		var frame svcproto.TerminalFrame
		_ = wire.SetReadDeadline(time.Now().Add(time.Minute))
		if wire.Receive(&frame) != nil {
			return
		}
		switch frame.Op {
		case "ping":
			if wire.Send(svcproto.TerminalFrame{Op: "pong"}) != nil {
				return
			}
		case "input":
			if len(frame.Data) > 32<<10 {
				return
			}
			if _, err := master.Write(frame.Data); err != nil {
				return
			}
		case "resize":
			if err := pty.Setsize(master, terminalSize(frame)); err != nil {
				return
			}
		default:
			return
		}
	}
}

func terminalSize(f svcproto.TerminalFrame) *pty.Winsize {
	return &pty.Winsize{Cols: uint16(max(1, min(f.Cols, 1000))), Rows: uint16(max(1, min(f.Rows, 1000))), X: uint16(max(0, min(f.Width*f.Cols, 65535))), Y: uint16(max(0, min(f.Height*f.Rows, 65535)))}
}
