package remote

import (
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// MaxLine bounds one message once decompressed; anything larger is refused
// rather than buffered without end.
const MaxLine = 32 << 20

var ErrLineTooLong = errors.New("remote: message too long")

// Conn carries JSON values, one per line, over a tunnel connection. After
// CompressSends the lines it sends are a flate stream, flushed after each
// one; the peer reads them after DecompressReceives. Send is safe for
// concurrent use; Receive is not.
type Conn struct {
	ctx              context.Context
	cancel           context.CancelFunc
	localAttachments *attachmentSession
	attachmentClient *attachmentClient
	dialPort         func(context.Context, uint16) (net.Conn, error)
	c                net.Conn
	r                *bufio.Reader
	wmu              sync.Mutex
	fw               *flate.Writer
	written          atomic.Int64
	once             sync.Once
	onClose          func()
}

func newConn(c net.Conn, onClose func()) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	return &Conn{ctx: ctx, cancel: cancel, c: c, r: bufio.NewReaderSize(c, 64<<10), onClose: onClose}
}

// countWriter counts the bytes that reach the tunnel.
type countWriter struct {
	w io.Writer
	n *atomic.Int64
}

func (cw countWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.n.Add(int64(n))
	return n, err
}

func (c *Conn) Send(v any) error {
	if req, ok := v.(Request); ok {
		return c.sendRequest(req)
	}
	if req, ok := v.(*Request); ok && req != nil {
		return c.sendRequest(*req)
	}
	return c.sendJSON(v)
}

func (c *Conn) sendJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(data) >= MaxLine {
		return ErrLineTooLong
	}
	data = append(data, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.c.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if c.fw != nil {
		if _, err := c.fw.Write(data); err != nil {
			return err
		}
		return c.fw.Flush()
	}
	_, err = io.Copy(countWriter{c.c, &c.written}, bytes.NewReader(data))
	return err
}

// CompressSends compresses what Send sends from now on.
func (c *Conn) CompressSends() error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.fw != nil {
		return nil
	}
	fw, err := flate.NewWriter(countWriter{c.c, &c.written}, flate.DefaultCompression)
	if err != nil {
		return err
	}
	c.fw = fw
	return nil
}

// DecompressReceives reads what the peer sends after its CompressSends.
// Call it from the goroutine that receives, after the last plain line.
func (c *Conn) DecompressReceives() {
	c.r = bufio.NewReaderSize(flate.NewReader(c.r), 64<<10)
}

// Written is how many bytes Send has put on the tunnel.
func (c *Conn) Written() int64 { return c.written.Load() }

func (c *Conn) Receive(v any) error {
	var line []byte
	for {
		chunk, err := c.r.ReadSlice('\n')
		if len(line)+len(chunk) > MaxLine {
			return ErrLineTooLong
		}
		line = append(line, chunk...)
		if err == nil {
			break
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return err
		}
	}
	if err := json.Unmarshal(line, v); err != nil {
		return err
	}
	if req, ok := v.(*Request); ok {
		return c.receiveImages(req)
	}
	return nil
}

// SetReadDeadline bounds the next Receive, as a heartbeat check.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.c.SetReadDeadline(t) }

// Context ends when the control connection closes, including a reconnect.
func (c *Conn) Context() context.Context { return c.ctx }

func (c *Conn) Close() error {
	var err error
	c.once.Do(func() {
		c.cancel()
		if c.attachmentClient != nil {
			c.attachmentClient.http.CloseIdleConnections()
		}
		if c.localAttachments != nil {
			c.localAttachments.close()
		}
		err = c.c.Close()
		if c.onClose != nil {
			c.onClose()
		}
	})
	return err
}

// FilesPort is an HTTP file server inside the desktop's Tailcat overlay.
const FilesPort = 4244

const ServicePort = 4245
const TerminalPort = 4246

func (c *Conn) DialTerminal(ctx context.Context) (*Conn, error) {
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	if c.dialPort == nil {
		return nil, errors.New("remote: terminal transport unavailable")
	}
	nc, err := c.dialPort(ctx, TerminalPort)
	if err != nil {
		return nil, err
	}
	return newConn(nc, nil), nil
}

// DialFiles reuses this connection's tunnel and identity. It never binds a
// public listening socket or consumes another phone key slot.
func (c *Conn) DialFiles(ctx context.Context) (net.Conn, error) {
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	if c.dialPort == nil {
		return nil, errors.New("remote: file transport unavailable")
	}
	return c.dialPort(ctx, FilesPort)
}
