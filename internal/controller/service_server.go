package controller

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"miya/internal/appdir"
	"miya/internal/appmodel"
	"miya/internal/async"
	"miya/internal/automation"
	mcpproto "miya/internal/mcp/protocol"
	"miya/internal/memory"
	"miya/internal/memory/manager"
	"miya/internal/model"
	"miya/internal/notification"
	phoneserver "miya/internal/phone/server"
	"miya/internal/prefs"
	"miya/internal/remote"
	"miya/internal/runtime/tracker"
	"miya/internal/svcproto"
	"miya/internal/uiruntime"
	"miya/internal/webservice"
)

// serviceIdleTimeout is how long a service with no window and no running
// turn waits before it exits.
const serviceIdleTimeout = 10 * time.Minute

type Service struct {
	C        *Controller
	exec     *async.Executor
	mu       sync.Mutex
	peers    map[*SvcPeer]bool
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
type SvcPeer struct {
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

func (p *SvcPeer) Write(data []byte) error {
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

func NewService(c *Controller, ex *async.Executor) *Service {
	c.runtimePath = filepath.Join(appdir.Dir(), "runtime.json")
	c.Memories().Store = memory.New(filepath.Join(appdir.Dir(), "memory"))
	s := &Service{C: c, exec: ex, peers: map[*SvcPeer]bool{}, Dirty: make(chan struct{}, 1), Quit: make(chan struct{}), IdleTimeout: serviceIdleTimeout}
	c.Push = notification.NewHub()
	c.Run = ex.Push
	c.OnDraftReturned = s.draftReturned
	c.Memories().Engine = NewMemoryEngine(c)
	c.Collaboration = NewCollaboration(c, filepath.Join(appdir.Dir(), "delegations"))
	return s
}

func (s *Service) MarkDirty() {
	select {
	case s.Dirty <- struct{}{}:
	default:
	}
}

func (s *Service) Stop() {
	s.quitOnce.Do(func() {
		if s.C.Collaboration != nil {
			s.C.Collaboration.Stop()
		}
		if s.C.Memories().Engine != nil {
			s.C.Memories().Engine.Stop()
		}
		close(s.Quit)
	})
}

func (s *Service) Serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go s.HandleConn(&SvcPeer{conn: conn})
	}
}

func (s *Service) HandleConn(p *SvcPeer) {
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

func (s *Service) request(p *SvcPeer, req svcproto.Request) {
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
func (s *Service) Handle(p *SvcPeer, req svcproto.Request) (any, error) {
	c := s.C
	arg := func(v any) error {
		if len(req.Args) == 0 {
			return nil
		}
		return json.Unmarshal(req.Args, v)
	}
	if mcpproto.IsOperation(req.Op) {
		var request mcpproto.Request
		if err := arg(&request); err != nil {
			return nil, err
		}
		return c.MCP().Handle(req.Op, request)
	}
	var id int
	var value string
	switch req.Op {
	case "host":
		var r svcproto.HostRequest
		if err := arg(&r); err != nil {
			return nil, err
		}
		return c.PrepareHost(r)
	case "commands":
		_ = arg(&value)
		list, known := c.ClaudeCommandsFor(value)
		return svcproto.HostCommands{Choices: list, Known: known}, nil
	case "notification-presence":
		var present bool
		if err := arg(&present); err != nil {
			return nil, err
		}
		if present && c.Push != nil {
			c.Push.NoteDesktopPresence(time.Now().Add(25 * time.Second))
		}
	case "hello":
		return svcproto.Hello{Version: svcproto.Version, Exe: s.exe, ExeTime: s.exeTime, PID: os.Getpid(), Busy: s.Busy(), Remote: s.Persistent}, nil
	case "runtimeValidate":
		var r webservice.Service
		if err := arg(&r); err != nil {
			return nil, err
		}
		manager := c.Runtimes()
		return async.Reply(func() (any, error) {
			ctx, cancel := context.WithTimeout(manager.Context(), 5*time.Second)
			defer cancel()
			return nil, manager.ValidateToken(ctx, r.RuntimeToken, r.Host, r.Port)
		}), nil
	case "runtime":
		var r tracker.Request
		if err := arg(&r); err != nil {
			return nil, err
		}
		return c.PrepareRuntime(r.Ref, r.Project)
	case "state":
		return c.SnapshotState(), nil
	case "desktop-read":
		var read appmodel.DesktopRead
		if err := arg(&read); err != nil {
			return nil, err
		}
		c.MarkDesktopRead(read)
		s.MarkDirty()
	case "subscribe":
		_ = arg(&id)
		p.Sub, p.sent, p.order = id, nil, nil
		s.MarkDirty()
	case "collaboration":
		var r appmodel.CollaborationRequest
		if err := arg(&r); err != nil {
			return nil, err
		}
		return c.CollaborationUserOp(r)
	case "memoryList", "memoryGet", "memorySave", "memoryForget", "memoryDelete", "memoryConfigure", "memorySessionConfigure", "memoryPromoteUser", "memoryPromotionPreview", "memoryPromoteFile":
		var r manager.Request
		if err := arg(&r); err != nil {
			return nil, err
		}
		return c.Memories().Op(req.Op, r)
	case "automationList", "automationDraft", "automationSave", "automationToggle", "automationDelete", "automationRunNow":
		var r automation.Request
		if err := arg(&r); err != nil {
			return nil, err
		}
		return c.AutomationOp(req.Op, r)
	case "prefetch":
		var ref model.ThreadRef
		if err := arg(&ref); err != nil {
			return nil, err
		}
		c.PrefetchReference(ref)
	case "submit":
		var r model.SendRequest
		if err := arg(&r); err != nil {
			return nil, err
		}
		id := c.Submit(r)
		if id == 0 && c.LastSubmitError != "" {
			return nil, fmt.Errorf("%s", c.LastSubmitError)
		}
		return id, nil
	case "retry":
		var r appmodel.RetryRequest
		if err := arg(&r); err != nil {
			return nil, err
		}
		return nil, c.RetryResponse(r)
	case "stop":
		_ = arg(&id)
		c.StopThread(id)
	case "archive", "delete", "open", "resume":
		_ = arg(&id)
		t := c.ThreadWithID(id)
		if t.ID == 0 {
			return nil, nil
		}
		switch req.Op {
		case "archive":
			c.ArchiveThread(t)
		case "delete":
			c.DeleteThread(t)
		case "open":
			c.BackendOf(t).Open(t)
		case "resume":
			c.Resume(t, nil)
		}
	case "respond":
		var a struct {
			ID       json.RawMessage `json:"id"`
			Result   json.RawMessage `json:"result"`
			Remember bool            `json:"remember,omitempty"`
		}
		_ = arg(&a)
		var result any
		_ = json.Unmarshal(a.Result, &result)
		for _, ap := range c.Approvals() {
			if string(ap.ID) == string(a.ID) {
				if a.Remember {
					return nil, c.RespondMCPAlways(ap)
				}
				c.Respond(ap, result)
				break
			}
		}
		if a.Remember {
			return nil, fmt.Errorf("Request already handled or expired")
		}
	case "rewind":
		var a svcproto.Rewind
		_ = arg(&a)
		c.Rewind(a.Thread, a.Message, a.Text)
	case "task":
		var a svcproto.TaskAction
		if err := json.Unmarshal(req.Args, &a); err != nil {
			return nil, err
		}
		return c.TaskAction(a.Thread, a.Task, a.Action), nil
	case "command":
		var a svcproto.Command
		_ = arg(&a)
		thread, message := c.ThreadCommand(a.Thread, a.Agent, a.Command, a.Policy)
		return svcproto.CommandResult{Thread: thread, Message: message, FastMode: c.fastMode}, nil
	case "workspace":
		_ = arg(&value)
		if err := c.ChangeWorkspace(value); err != nil {
			return svcproto.WorkspaceResult{Error: err.Error()}, nil
		}
		return svcproto.WorkspaceResult{Cwd: c.Cwd}, nil
	case "connect":
		c.Connect()
	case "model", "effort", "policy", "locale", "claudeCommand":
		_ = arg(&value)
		switch req.Op {
		case "model":
			c.SelectModel(value)
		case "effort":
			c.setCodexEffort(value)
		case "policy":
			c.SetPermissionPolicy(value)
		case "locale":
			c.SetLocale(value)
		case "claudeCommand":
			c.SetClaudeCommand(value)
		}
	case "threadPolicy", "draft":
		var a svcproto.ThreadValue
		_ = arg(&a)
		if a.Thread != 0 {
			c.Update(a.Thread, func(t *model.Thread) {
				if req.Op == "draft" {
					t.Draft = a.Value
				} else {
					t.PermissionPolicy = prefs.NormalizePermissionPolicy(a.Value)
				}
			})
		}
	case "renameProject", "archiveProject", "deleteProject":
		var a svcproto.ProjectRequest
		_ = arg(&a)
		switch req.Op {
		case "renameProject":
			c.RenameProject(a.Path, a.Name)
		case "archiveProject":
			c.ArchiveProject(a.Path)
		case "deleteProject":
			c.DeleteProject(a.Path)
		}
	case "phone":
		_ = arg(&value)
		DesktopPhone.Do(value)
		uiruntime.Invalidate()
	case "dequeue":
		var a svcproto.Queued
		_ = arg(&a)
		c.RemoveQueued(a.Thread, a.ID)
	case "steerQueued":
		var a svcproto.Queued
		_ = arg(&a)
		c.SteerQueued(a.Thread, a.ID)
	case "drainQueue":
		_ = arg(&id)
		c.ResumeQueue(id)
	case "claudeModel":
		_ = arg(&value)
		c.setClaudeModel(value)
	case "claudeEffort":
		_ = arg(&value)
		c.setClaudeEffort(value)
	case "account":
		c.RefreshAccount()
	case "accountAsk":
		c.AskAccountReset()
	case "accountCancel":
		c.CancelAccountReset()
	case "accountConfirm":
		c.ConfirmAccountReset()
	case "quit":
		s.Stop()
	case "quitWhenIdle":
		s.QuitWhenIdle = true
		if c.Memories().Engine != nil {
			c.Memories().Engine.Pause()
		}
		if !s.Busy() {
			s.Stop()
		}
	default:
		return nil, fmt.Errorf("unknown operation %q", req.Op)
	}
	return nil, nil
}

// busy reports a turn running or starting, which quitting would end.
func (s *Service) Busy() bool {
	if s.C.Collaboration != nil && s.C.Collaboration.Context().Err() == nil && s.C.Collaboration.Pending() > 0 {
		return true
	}
	if s.C.RemoteTerminals.Load() > 0 {
		return true
	}
	if s.C.desktopBusy() {
		return true
	}
	if s.C.runtimeBusy() {
		return true
	}
	if s.C.Memories().Calls > 0 || s.C.Memories().Engine != nil && s.C.Memories().Engine.Busy() || len(s.C.ClaudeRuns) > 0 {
		return true
	}
	for _, t := range s.C.Threads() {
		if t.Busy || t.TaskPending || model.ActiveGoal(t) {
			return true
		}
	}
	for path, threads := range s.C.ProjectCache {
		if path == s.C.Cwd {
			continue
		}
		for _, t := range threads {
			if t.Busy || t.TaskPending || model.ActiveGoal(t) {
				return true
			}
		}
	}
	return false
}

func (s *Service) peerList() []*SvcPeer {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*SvcPeer, 0, len(s.peers))
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
	st := s.C.SnapshotState()
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

func (s *Service) publishMessages(p *SvcPeer) {
	t := s.C.ThreadWithID(p.Sub)
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
func (p *SvcPeer) Diff(t model.Thread) (svcproto.ThreadMessages, bool) {
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
func (s *Service) draftReturned(id int, text string, attachments []string) {
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
	if s.Persistent || busy || len(s.peerList()) > 0 || DesktopPhone.Connected() || s.C.Auto.Enabled() {
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
func (c *Controller) SnapshotState() svcproto.State {
	busy := make(map[string]bool)
	for _, p := range c.Projects() {
		if c.ProjectBusy(p.Path) {
			busy[p.Path] = true
		}
	}
	threads := make([]model.Thread, len(c.Threads()))
	for i, t := range c.Threads() {
		t.Messages = nil
		threads[i] = t
	}
	projectThreads := make(map[string][]model.Thread, len(c.ProjectCache))
	for path, cached := range c.ProjectCache {
		if path == c.Cwd {
			continue
		}
		items := make([]model.Thread, len(cached))
		for i, t := range cached {
			t = phoneserver.ListThread(t)
			t.Queue = nil
			items[i] = t
		}
		projectThreads[path] = items
	}
	return svcproto.State{
		Collaboration:   c.CollaborationCapabilities(),
		ReferenceSource: c.ReferenceSource,
		MemoryRevision:  c.MemoryRevision,
		FastMode:        c.fastMode,
		ProjectBusy:     busy,
		ProjectThreads:  projectThreads,
		DesktopUnread:   c.DesktopUnread,
		Cwd:             c.Cwd, Projects: c.Projects(), Threads: threads, Approvals: c.Approvals(),
		Status: c.Status(), HistoryStatus: c.HistoryStatus(), Ready: c.Ready(),
		Models: c.Models(), Model: c.Model(), Effort: c.Effort(), PermissionPolicy: c.PermissionPolicy(), Locale: c.Locale(),
		ClaudeCommand: c.ClaudeCommand, ClaudeError: c.ClaudeError, ClaudeBinary: c.ClaudeBinary, ClaudeArgs: c.ClaudeArgs, ClaudeEnv: c.ClaudeEnv, ClaudeModel: c.ClaudeModel, ClaudeEffort: c.ClaudeEffort, ClaudeDefaultModel: c.claudeDefaultModel,
		Account: c.AccountState, AccountReset: c.AccountReset, QuotaLimits: c.QuotaLimits, QuotaStatus: c.quotaStatus,
		Phone: DesktopPhone.Status(),
	}
}
