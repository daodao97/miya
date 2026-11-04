package main

import (
	"context"
	"errors"
	"io"
	"time"

	"miya/internal/remote"
)

type terminalFrame struct {
	Op                        string `json:"op"`
	Project                   string `json:"project,omitempty"`
	Data                      []byte `json:"data,omitempty"`
	Cols, Rows, Width, Height int
	Error                     string `json:"error,omitempty"`
}

type remoteTerminal struct {
	wire    *remote.Conn
	pending []byte
}

func openRemoteTerminal(cl *serviceClient, project string) (io.ReadWriteCloser, error) {
	if cl == nil || cl.wire == nil {
		return nil, errServiceClosed
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wire, err := cl.wire.DialTerminal(ctx)
	if err != nil {
		return nil, err
	}
	_ = wire.SetReadDeadline(time.Now().Add(20 * time.Second))
	err = wire.Send(terminalFrame{Op: "open", Project: project, Cols: 80, Rows: 24})
	var reply terminalFrame
	if err == nil {
		err = wire.Receive(&reply)
	}
	if err == nil && reply.Error != "" {
		err = errors.New(reply.Error)
	}
	if err == nil && reply.Op != "ready" {
		err = errors.New("Incompatible server terminal protocol")
	}
	if err != nil {
		wire.Close()
		return nil, err
	}
	_ = wire.SetReadDeadline(time.Time{})
	t := &remoteTerminal{wire: wire}
	// A shell must end with the owning desktop connection, including a
	// network failure discovered by the service heartbeat.
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-cl.closed:
				t.Close()
				return
			case <-wire.Context().Done():
				return
			case <-ticker.C:
				if wire.Send(terminalFrame{Op: "ping"}) != nil {
					t.Close()
					return
				}
			}
		}
	}()
	return t, nil
}

func (t *remoteTerminal) Read(p []byte) (int, error) {
	for len(t.pending) == 0 {
		var frame terminalFrame
		_ = t.wire.SetReadDeadline(time.Now().Add(time.Minute))
		if err := t.wire.Receive(&frame); err != nil {
			return 0, err
		}
		if frame.Error != "" {
			return 0, errors.New(frame.Error)
		}
		if frame.Op == "exit" {
			return 0, io.EOF
		}
		if frame.Op == "output" {
			t.pending = frame.Data
		}
	}
	n := copy(p, t.pending)
	t.pending = t.pending[n:]
	return n, nil
}

func (t *remoteTerminal) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := min(len(p), 32<<10)
		if err := t.wire.Send(terminalFrame{Op: "input", Data: p[:n]}); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}
func (t *remoteTerminal) Resize(cols, rows int) error { return t.ResizePixels(cols, rows, 0, 0) }
func (t *remoteTerminal) ResizePixels(cols, rows, width, height int) error {
	return t.wire.Send(terminalFrame{Op: "resize", Cols: cols, Rows: rows, Width: width, Height: height})
}
func (t *remoteTerminal) Close() error { return t.wire.Close() }
