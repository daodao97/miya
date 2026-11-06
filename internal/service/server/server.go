package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"miya/internal/async"
	"miya/internal/chat/model"
	"miya/internal/remote"
	svcproto "miya/internal/service/protocol"
)

// serviceIdleTimeout is how long a service with no window and no running
// turn waits before it exits.
const serviceIdleTimeout = 10 * time.Minute

type Service struct {
	host     Host
	exec     *async.Executor
	mu       sync.Mutex
	peers    map[*Peer]bool
	Dirty    chan struct{}
	Quit     chan struct{}
	quitOnce sync.Once
	exe      string
	exeTime  time.Time

	// Owned by the executor.
	IdleSince    time.Time
	QuitWhenIdle bool
	IdleTimeout  time.Duration
	Persistent   bool
}

// svcPeer is a connected window.
type Peer struct {
	conn     net.Conn
	Wire     *remote.Conn
	out      chan []byte
	queued   atomic.Int64
	inflight atomic.Int32
	wmu      sync.Mutex

	// Owned by the executor: the subscribed conversation and what the
	// window has of it.
	Sub       int
	sent      map[int]uint64
	order     []int
	lastState []byte
}

func (p *Peer) Write(data []byte) error {
	if p.Wire != nil {
		if p.queued.Add(int64(len(data))) <= 64<<20 {
			select {
			case p.out <- data:
				return nil
			default:
			}
		}
		p.queued.Add(-int64(len(data)))
		p.Wire.Close()
		return svcproto.ErrClosed
	}
	line := make([]byte, len(data)+1)
	copy(line, data)
	line[len(data)] = '\n'
	p.wmu.Lock()
	defer p.wmu.Unlock()
	_ = p.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := p.conn.Write(line)
	if err != nil {
		p.conn.Close()
	}
	return err
}

func (s *Service) MarkDirty() {
	select {
	case s.Dirty <- struct{}{}:
	default:
	}
}

func (s *Service) Serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go s.HandleConn(&Peer{conn: conn})
	}
}

func (s *Service) HandleConn(p *Peer) {
	if p.Wire != nil {
		p.out = make(chan []byte, 64)
		go func() {
			for {
				select {
				case <-p.Wire.Context().Done():
					return
				case data := <-p.out:
					err := p.Wire.Send(json.RawMessage(data))
					p.queued.Add(-int64(len(data)))
					if err != nil {
						p.Wire.Close()
						return
					}
				}
			}
		}()
	}
	s.mu.Lock()
	s.peers[p] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.peers, p)
		s.mu.Unlock()
		if p.Wire != nil {
			p.Wire.Close()
		} else {
			p.conn.Close()
		}
	}()
	var r *bufio.Reader
	if p.Wire == nil {
		r = bufio.NewReaderSize(p.conn, 64<<10)
	}
	for {
		var line []byte
		var err error
		if p.Wire != nil {
			_ = p.Wire.SetReadDeadline(time.Now().Add(time.Minute))
			err = p.Wire.Receive((*json.RawMessage)(&line))
		} else {
			line, err = r.ReadBytes('\n')
		}
		if len(bytes.TrimSpace(line)) > 0 {
			var req svcproto.Request
			if json.Unmarshal(line, &req) == nil {
				s.request(p, req)
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *Service) request(p *Peer, req svcproto.Request) {
	if req.ID == 0 {
		s.exec.Push(func() { _, _ = s.Handle(p, req) })
		return
	}
	if p.Wire != nil && p.inflight.Add(1) > 32 {
		p.inflight.Add(-1)
		p.Wire.Close()
		return
	}
	done := make(chan svcproto.Envelope, 1)
	s.exec.Push(func() {
		res, err := s.Handle(p, req)
		if d, ok := res.(async.Reply); ok && err == nil {
			// Work that waits on other programs runs off the executor.
			go func() {
				res, err := d()
				done <- svcReply(req, res, err)
			}()
			return
		}
		done <- svcReply(req, res, err)
	})
	wait := func() {
		var disconnected <-chan struct{}
		if p.Wire != nil {
			disconnected = p.Wire.Context().Done()
			defer p.inflight.Add(-1)
		}
		select {
		case msg := <-done:
			if data, err := json.Marshal(msg); err == nil {
				_ = p.Write(data)
			}
		case <-disconnected:
		case <-s.Quit:
		}
	}
	// Keep accepting remote heartbeats and stop/approval requests while a
	// deferred operation (such as drafting an automation) waits on an agent.
	// Enqueue above, before spawning the waiter, to preserve request order.
	if p.Wire != nil {
		go wait()
	} else {
		wait()
	}
}

// svcReply is the reply to a request.
func svcReply(req svcproto.Request, res any, err error) svcproto.Envelope {
	msg := svcproto.Envelope{ID: req.ID}
	if err != nil {
		msg.Error = err.Error()
	} else if res != nil {
		msg.Result, _ = json.Marshal(res)
	}
	return msg
}

// handle runs a window's request on the executor.
func (s *Service) peerList() []*Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Peer, 0, len(s.peers))
	for p := range s.peers {
		out = append(out, p)
	}
	return out
}

// A change publishes publishDelay after it, gathering the changes that
// follow; changes that keep coming, as a streaming reply's, publish at most
// once every publishGap, which still reads as smooth text but costs every
// window (a JSON snapshot, a rebuilt view) far fewer frames.
const (
	publishDelay = 30 * time.Millisecond
	publishGap   = 80 * time.Millisecond
)

// publishLoop sends what changed, as publishDelay and publishGap pace it.
func (s *Service) PublishLoop() {
	var last time.Time
	for {
		select {
		case <-s.Quit:
			return
		case <-s.Dirty:
		}
		wait := publishDelay
		if gap := publishGap - time.Since(last); gap > wait {
			wait = gap
		}
		t := time.NewTimer(wait)
		select {
		case <-s.Quit:
			t.Stop()
			return
		case <-t.C:
		}
		last = time.Now()
		s.exec.Push(s.publish)
	}
}

func (s *Service) publish() {
	st := s.host.SnapshotState()
	data, err := json.Marshal(svcproto.Envelope{Event: "state", State: &st})
	if err != nil {
		return
	}
	for _, p := range s.peerList() {
		if !bytes.Equal(p.lastState, data) {
			if p.Write(data) == nil {
				p.lastState = data
			}
		}
		if p.Sub != 0 {
			s.publishMessages(p)
		}
	}
}

func (s *Service) publishMessages(p *Peer) {
	t := s.host.ThreadWithID(p.Sub)
	if t.ID == 0 {
		return
	}
	if update, ok := p.Diff(t); ok {
		if data, err := json.Marshal(svcproto.Envelope{Event: "messages", Messages: &update}); err == nil {
			_ = p.Write(data)
		}
	}
}

// diff is the update that brings the window's copy of t up to date, and
// whether there is one; it records the update as sent.
func (p *Peer) Diff(t model.Thread) (svcproto.ThreadMessages, bool) {
	update := svcproto.ThreadMessages{Thread: t.ID, Reset: p.sent == nil}
	if update.Reset {
		p.sent = map[int]uint64{}
	}
	same := len(t.Messages) == len(p.order)
	update.Order = make([]int, len(t.Messages))
	for i, m := range t.Messages {
		update.Order[i] = m.ID
		if same && p.order[i] != m.ID {
			same = false
		}
		if fp := svcproto.MessageFingerprint(m); p.sent[m.ID] != fp {
			p.sent[m.ID] = fp
			update.Changed = append(update.Changed, m)
		}
	}
	if !update.Reset && same && len(update.Changed) == 0 {
		return svcproto.ThreadMessages{}, false
	}
	p.order = update.Order
	return update, true
}

// draftReturned gives a message that could not be sent to every window;
// the one showing its conversation puts it back in its composer.
func (s *Service) DraftReturned(id int, text string, attachments []string) {
	data, err := json.Marshal(svcproto.Envelope{Event: "draft", Draft: &svcproto.Draft{Thread: id, Text: text, Attachments: attachments}})
	if err != nil {
		return
	}
	for _, p := range s.peerList() {
		_ = p.Write(data)
	}
}

// watchIdle ends the service once nothing needs it.
func (s *Service) watchIdle(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.Quit:
			return
		case <-t.C:
		}
		s.exec.Push(s.CheckIdle)
	}
}

func (s *Service) CheckIdle() {
	busy := s.Busy()
	if s.QuitWhenIdle && !busy {
		s.Stop()
		return
	}
	if s.Persistent || busy || len(s.peerList()) > 0 || s.host.ServiceKeepAlive() {
		s.IdleSince = time.Time{}
		return
	}
	if s.IdleSince.IsZero() {
		s.IdleSince = time.Now()
	} else if time.Since(s.IdleSince) >= s.IdleTimeout {
		s.Stop()
	}
}

// snapshotState is the state windows show, without messages.

func New(host Host, ex *async.Executor) *Service {
	return &Service{host: host, exec: ex, peers: map[*Peer]bool{}, Dirty: make(chan struct{}, 1), Quit: make(chan struct{}), IdleTimeout: serviceIdleTimeout}
}
func (s *Service) SetExecutable(path string, at time.Time) { s.exe, s.exeTime = path, at }
func (s *Service) Stop()                                   { s.quitOnce.Do(func() { s.host.ServiceStop(); close(s.Quit) }) }
func (s *Service) Busy() bool                              { return s.host.ServiceBusy() }
func (s *Service) Handle(p *Peer, req svcproto.Request) (any, error) {
	switch req.Op {
	case "hello":
		return svcproto.Hello{Version: svcproto.Version, Exe: s.exe, ExeTime: s.exeTime, PID: os.Getpid(), Busy: s.Busy(), Remote: s.Persistent}, nil
	case "state":
		return s.host.SnapshotState(), nil
	case "subscribe":
		var id int
		_ = json.Unmarshal(req.Args, &id)
		p.Sub, p.sent, p.order = id, nil, nil
		s.MarkDirty()
		return nil, nil
	case "quit":
		s.Stop()
		return nil, nil
	case "quitWhenIdle":
		s.QuitWhenIdle = true
		s.host.ServicePause()
		if !s.Busy() {
			s.Stop()
		}
		return nil, nil
	default:
		result, err := s.host.ServiceRequest(req)
		if req.Op == "desktop-read" {
			s.MarkDirty()
		}
		return result, err
	}
}
