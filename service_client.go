package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"miya/internal/remote"
	"net"
	"sync"
	"time"
)

var errServiceClosed = errors.New("Background service disconnected")

// serviceClient is a window's connection to the service.
type serviceClient struct {
	conn    net.Conn
	wire    *remote.Conn
	wmu     sync.Mutex
	mu      sync.Mutex
	next    int64
	pending map[int64]chan svcMessage
	events  func(svcMessage)
	closed  chan struct{}
	once    sync.Once
}

func newServiceClient(conn net.Conn) *serviceClient {
	cl := &serviceClient{conn: conn, pending: map[int64]chan svcMessage{}, closed: make(chan struct{})}
	go cl.read()
	return cl
}

// newWireServiceClient is a connection to a remote server's service, over
// the remote link instead of the local socket.
func newWireServiceClient(wire *remote.Conn) *serviceClient {
	cl := &serviceClient{wire: wire, pending: map[int64]chan svcMessage{}, closed: make(chan struct{})}
	go cl.read()
	return cl
}

// newDetachedServiceClient has no connection yet; calls fail once it is
// closed.
func newDetachedServiceClient() *serviceClient {
	return &serviceClient{pending: map[int64]chan svcMessage{}, closed: make(chan struct{})}
}

// newClosedServiceClient keeps a window on the service path while it waits
// for one: every call fails at once.
func newClosedServiceClient() *serviceClient {
	cl := newDetachedServiceClient()
	cl.Close()
	return cl
}

// Done is closed once the connection ends.
func (cl *serviceClient) Done() <-chan struct{} { return cl.closed }

// Wire is the remote link of a connection to a remote server, nil for the
// local socket.
func (cl *serviceClient) Wire() *remote.Conn {
	if cl == nil {
		return nil
	}
	return cl.wire
}

// HasTransport reports whether the connection was made over a socket or a
// remote link, open or not; a detached placeholder has none.
func (cl *serviceClient) HasTransport() bool {
	return cl != nil && (cl.conn != nil || cl.wire != nil)
}

// Connected reports whether the connection has a transport and is open.
func (cl *serviceClient) Connected() bool {
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

func dialService(sock string) (*serviceClient, error) {
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		return nil, err
	}
	return newServiceClient(conn), nil
}

// setEvents sets what receives the service's events, on the reading goroutine.
func (cl *serviceClient) setEvents(f func(svcMessage)) {
	cl.mu.Lock()
	cl.events = f
	cl.mu.Unlock()
}

func (cl *serviceClient) read() {
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
			var m svcMessage
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
func (cl *serviceClient) Close() {
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
			ch <- svcMessage{ID: id, Error: errServiceClosed.Error()}
			delete(cl.pending, id)
		}
		cl.mu.Unlock()
	})
}

func (cl *serviceClient) send(req svcRequest) error {
	if cl == nil || cl.conn == nil && cl.wire == nil {
		return errServiceClosed
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
func (cl *serviceClient) call(op string, args, out any) error {
	if cl == nil {
		return errServiceClosed
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	ch := make(chan svcMessage, 1)
	cl.mu.Lock()
	select {
	case <-cl.closed:
		cl.mu.Unlock()
		return errServiceClosed
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
	if err := cl.send(svcRequest{ID: id, Op: op, Args: raw}); err != nil {
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
	case <-time.After(serviceCallTimeout(op)):
		forget()
		return errors.New("Background service is not responding")
	}
}

// serviceCallTimeout is how long a request may take: most answer at once,
// a draft waits for an agent, and a confirmation card for the person.
func serviceCallTimeout(op string) time.Duration {
	switch op {
	case "automationDraft":
		return 3 * time.Minute
	case "mcpDesktopControl", "mcpAutomationCreate", "mcpAutomationUpdate":
		return miyaRequestTimeout + time.Minute
	}
	return 15 * time.Second
}

// notify sends a request without waiting.
func (cl *serviceClient) notify(op string, args any) {
	if cl == nil {
		return
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return
	}
	_ = cl.send(svcRequest{Op: op, Args: raw})
}

// connectService connects to the service, starting it when none runs, and
// replaces one built from another executable once it is idle.
func connectService(workspace string) (*serviceClient, svcState, error) {
	sock := serviceSocket()
	cl, err := dialService(sock)
	if err != nil {
		if cl, err = startService(sock, workspace); err != nil {
			return nil, svcState{}, err
		}
	}
	exe, exeTime := executableInfo()
	var h svcHello
	if err := cl.call("hello", nil, &h); err != nil {
		cl.Close()
		return nil, svcState{}, err
	}
	if h.Remote && h.Version != serviceVersion {
		cl.Close()
		return nil, svcState{}, errors.New("Incompatible server version. Update using the server installation process.")
	}
	if !h.Remote && (h.Version != serviceVersion || h.Exe != exe || !h.ExeTime.Equal(exeTime)) {
		switch {
		case !h.Busy:
			_ = cl.call("quit", nil, nil)
			cl.Close()
			waitProcessGone(h.PID, 3*time.Second)
			if cl, err = startService(sock, workspace); err != nil {
				return nil, svcState{}, err
			}
		case h.Version == serviceVersion:
			// A turn runs in the older service: use it, and let it end once idle.
			cl.notify("quitWhenIdle", nil)
		default:
			// Keep the old writer until its work ends, then let a waiting
			// window start the new service. Never create a second writer.
			cl.notify("quitWhenIdle", nil)
			cl.Close()
			return nil, svcState{}, errors.New("Incompatible background service version; conversations are running")
		}
	}
	var st svcState
	if err := cl.call("state", nil, &st); err != nil {
		cl.Close()
		return nil, svcState{}, err
	}
	return cl, st, nil
}

func startService(sock, workspace string) (*serviceClient, error) {
	if err := spawnService(workspace); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		time.Sleep(30 * time.Millisecond)
		cl, err := dialService(sock)
		if err == nil {
			return cl, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
	}
}
