package svcproto

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"

	mcpproto "miya/internal/mcp/protocol"
	"miya/internal/remote"
)

// CardTimeout is how long a confirmation card waits for the person; then it
// counts as declined. Calls that show one wait longer than this.
const CardTimeout = 10 * time.Minute

var ErrClosed = errors.New("Background service disconnected")

// Client is a window's connection to the service.
type Client struct {
	conn    net.Conn
	wire    *remote.Conn
	wmu     sync.Mutex
	mu      sync.Mutex
	next    int64
	pending map[int64]chan Envelope
	events  func(Envelope)
	closed  chan struct{}
	once    sync.Once
}

func NewClient(conn net.Conn) *Client {
	cl := &Client{conn: conn, pending: map[int64]chan Envelope{}, closed: make(chan struct{})}
	go cl.read()
	return cl
}

// NewWireClient is a connection to a remote server's service, over
// the remote link instead of the local socket.
func NewWireClient(wire *remote.Conn) *Client {
	cl := &Client{wire: wire, pending: map[int64]chan Envelope{}, closed: make(chan struct{})}
	go cl.read()
	return cl
}

// NewDetachedClient has no connection yet; calls fail once it is
// closed.
func NewDetachedClient() *Client {
	return &Client{pending: map[int64]chan Envelope{}, closed: make(chan struct{})}
}

// NewClosedClient keeps a window on the service path while it waits
// for one: every call fails at once.
func NewClosedClient() *Client {
	cl := NewDetachedClient()
	cl.Close()
	return cl
}

// Done is closed once the connection ends.
func (cl *Client) Done() <-chan struct{} { return cl.closed }

// Wire is the remote link of a connection to a remote server, nil for the
// local socket.
func (cl *Client) Wire() *remote.Conn {
	if cl == nil {
		return nil
	}
	return cl.wire
}

// HasTransport reports whether the connection was made over a socket or a
// remote link, open or not; a detached placeholder has none.
func (cl *Client) HasTransport() bool {
	return cl != nil && (cl.conn != nil || cl.wire != nil)
}

// Connected reports whether the connection has a transport and is open.
func (cl *Client) Connected() bool {
	if !cl.HasTransport() {
		return false
	}
	select {
	case <-cl.closed:
		return false
	default:
		return true
	}
}

func Dial(sock string) (*Client, error) {
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		return nil, err
	}
	return NewClient(conn), nil
}

// setEvents sets what receives the service's events, on the reading goroutine.
func (cl *Client) SetEvents(f func(Envelope)) {
	cl.mu.Lock()
	cl.events = f
	cl.mu.Unlock()
}

func (cl *Client) read() {
	defer cl.Close()
	var r *bufio.Reader
	if cl.wire == nil {
		r = bufio.NewReaderSize(cl.conn, 64<<10)
	}
	for {
		var line []byte
		var err error
		if cl.wire != nil {
			_ = cl.wire.SetReadDeadline(time.Now().Add(time.Minute))
			err = cl.wire.Receive((*json.RawMessage)(&line))
		} else {
			line, err = r.ReadBytes('\n')
		}
		if len(bytes.TrimSpace(line)) > 0 {
			var m Envelope
			if json.Unmarshal(line, &m) == nil {
				cl.mu.Lock()
				ch, events := cl.pending[m.ID], cl.events
				if m.ID != 0 {
					delete(cl.pending, m.ID)
				}
				cl.mu.Unlock()
				switch {
				case m.ID != 0 && ch != nil:
					ch <- m
				case m.ID == 0 && events != nil:
					events(m)
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// Close ends the connection; waiting calls fail.
func (cl *Client) Close() {
	if cl == nil {
		return
	}
	cl.once.Do(func() {
		close(cl.closed)
		if cl.conn != nil {
			cl.conn.Close()
		}
		if cl.wire != nil {
			cl.wire.Close()
		}
		cl.mu.Lock()
		for id, ch := range cl.pending {
			ch <- Envelope{ID: id, Error: ErrClosed.Error()}
			delete(cl.pending, id)
		}
		cl.mu.Unlock()
	})
}

func (cl *Client) Send(req Request) error {
	if cl == nil || cl.conn == nil && cl.wire == nil {
		return ErrClosed
	}
	if cl.wire != nil {
		return cl.wire.Send(req)
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	cl.wmu.Lock()
	defer cl.wmu.Unlock()
	_ = cl.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err = cl.conn.Write(append(data, '\n'))
	return err
}

// call sends a request and waits for its result.
func (cl *Client) Call(op string, args, out any) error {
	if cl == nil {
		return ErrClosed
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	ch := make(chan Envelope, 1)
	cl.mu.Lock()
	select {
	case <-cl.closed:
		cl.mu.Unlock()
		return ErrClosed
	default:
	}
	cl.next++
	id := cl.next
	cl.pending[id] = ch
	cl.mu.Unlock()
	forget := func() {
		cl.mu.Lock()
		delete(cl.pending, id)
		cl.mu.Unlock()
	}
	if err := cl.Send(Request{ID: id, Op: op, Args: raw}); err != nil {
		forget()
		return err
	}
	select {
	case m := <-ch:
		if m.Error != "" {
			return errors.New(m.Error)
		}
		if out != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-time.After(CallTimeout(op)):
		forget()
		return errors.New("Background service is not responding")
	}
}

// CallTimeout is how long a request may take: most answer at once,
// a draft waits for an agent, and a confirmation card for the person.
func CallTimeout(op string) time.Duration {
	if mcpproto.WaitsForUser(op) {
		return CardTimeout + time.Minute
	}
	switch op {
	case "automationDraft":
		return 3 * time.Minute
	}
	return 15 * time.Second
}

// notify sends a request without waiting.
func (cl *Client) Notify(op string, args any) {
	if cl == nil {
		return
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return
	}
	_ = cl.Send(Request{Op: op, Args: raw})
}

// Inbox gathers the events a service pushes between two turns of the
// main thread, so that a burst of them costs one dispatch and one frame
// rather than one each. States are whole snapshots: of a batch only the
// latest is applied, in its place; messages and drafts are applied in order.
type Inbox struct {
	mu        sync.Mutex
	queued    []Envelope
	scheduled bool
}

// push queues m and reports whether the caller must schedule a drain: only
// the first event since the last take does.
func (b *Inbox) Push(m Envelope) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.queued = append(b.queued, m)
	if b.scheduled {
		return false
	}
	b.scheduled = true
	return true
}

// take returns the queued events in order, without the states a later state
// of the same batch replaces.
func (b *Inbox) Take() []Envelope {
	b.mu.Lock()
	queued := b.queued
	b.queued, b.scheduled = nil, false
	b.mu.Unlock()
	last := -1
	for i, m := range queued {
		if m.State != nil {
			last = i
		}
	}
	out := queued[:0]
	for i, m := range queued {
		if m.State != nil && i != last {
			continue
		}
		out = append(out, m)
	}
	return out
}
