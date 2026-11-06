//go:build !windows && !ios

package main

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"

	"miya/internal/async"
	"miya/internal/remote"

	"github.com/creack/pty"
)

func (c *chatController) serveRemoteTerminal(ctx context.Context, wire *remote.Conn) {
	_ = wire.SetReadDeadline(time.Now().Add(15 * time.Second))
	var open terminalFrame
	if wire.Receive(&open) != nil || open.Op != "open" {
		return
	}
	_ = wire.SetReadDeadline(time.Time{})
	known, ok := async.OnMain(ctx, c.dispatch, func() bool {
		if open.Project == "" {
			return false
		}
		known := open.Project == c.cwd
		for _, p := range c.projects() {
			known = known || p.Path == open.Project
		}
		return known
	})
	if !ok || !known {
		_ = wire.Send(terminalFrame{Op: "ready", Error: "Project is not in the server's project list"})
		return
	}
	c.remoteTerminals.Add(1)
	defer func() { c.remoteTerminals.Add(-1); invalidate() }()
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-l")
	cmd.Dir = open.Project
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	master, err := pty.StartWithSize(cmd, terminalSize(open))
	if err != nil {
		_ = wire.Send(terminalFrame{Op: "ready", Error: err.Error()})
		return
	}
	done := make(chan struct{})
	defer close(done)
	defer func() {
		master.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP)
		go cmd.Wait()
	}()
	if wire.Send(terminalFrame{Op: "ready"}) != nil {
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
			if n > 0 && wire.Send(terminalFrame{Op: "output", Data: buf[:n]}) != nil {
				wire.Close()
				return
			}
			if err != nil {
				_ = wire.Send(terminalFrame{Op: "exit"})
				wire.Close()
				return
			}
		}
	}()
	for {
		var frame terminalFrame
		_ = wire.SetReadDeadline(time.Now().Add(time.Minute))
		if wire.Receive(&frame) != nil {
			return
		}
		switch frame.Op {
		case "ping":
			if wire.Send(terminalFrame{Op: "pong"}) != nil {
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

func terminalSize(f terminalFrame) *pty.Winsize {
	return &pty.Winsize{Cols: uint16(max(1, min(f.Cols, 1000))), Rows: uint16(max(1, min(f.Rows, 1000))), X: uint16(max(0, min(f.Width*f.Cols, 65535))), Y: uint16(max(0, min(f.Height*f.Rows, 65535)))}
}
