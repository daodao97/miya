package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"miya/internal/memory"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// serviceIdleTimeout is how long a service with no window and no running
// turn waits before it exits.
const serviceIdleTimeout = 10 * time.Minute

type service struct {
	c        *chatController
	exec     *executor
	mu       sync.Mutex
	peers    map[*svcPeer]bool
	dirty    chan struct{}
	quit     chan struct{}
	quitOnce sync.Once
	exe      string
	exeTime  time.Time

	// Owned by the executor.
	idleSince    time.Time
	quitWhenIdle bool
	idleTimeout  time.Duration
}

// svcPeer is a connected window.
type svcPeer struct {
	conn net.Conn
	wmu  sync.Mutex

	// Owned by the executor: the subscribed conversation and what the
	// window has of it.
	sub       int
	sent      map[int]uint64
	order     []int
	lastState []byte
}

func (p *svcPeer) write(data []byte) error {
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

func newService(c *chatController, ex *executor) *service {
	c.runtimePath = filepath.Join(serviceDir(), "runtime.json")
	c.memory = memory.New(filepath.Join(serviceDir(), "memory"))
	s := &service{c: c, exec: ex, peers: map[*svcPeer]bool{}, dirty: make(chan struct{}, 1), quit: make(chan struct{}), idleTimeout: serviceIdleTimeout}
	c.push = newPushHub()
	c.run = ex.push
	c.onDraftReturned = s.draftReturned
	c.memoryEngine = newMemoryEngine(c)
	return s
}

func (s *service) markDirty() {
	select {
	case s.dirty <- struct{}{}:
	default:
	}
}

func (s *service) stop() {
	s.quitOnce.Do(func() {
		if s.c.memoryEngine != nil {
			s.c.memoryEngine.cancel()
		}
		close(s.quit)
	})
}

func (s *service) serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go s.handleConn(&svcPeer{conn: conn})
	}
}

func (s *service) handleConn(p *svcPeer) {
	s.mu.Lock()
	s.peers[p] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.peers, p)
		s.mu.Unlock()
		p.conn.Close()
	}()
	r := bufio.NewReaderSize(p.conn, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var req svcRequest
			if json.Unmarshal(line, &req) == nil {
				s.request(p, req)
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *service) request(p *svcPeer, req svcRequest) {
	if req.ID == 0 {
		s.exec.push(func() { _, _ = s.handle(p, req) })
		return
	}
	done := make(chan svcMessage, 1)
	s.exec.push(func() {
		res, err := s.handle(p, req)
		if d, ok := res.(deferred); ok && err == nil {
			// Work that waits on other programs runs off the executor.
			go func() {
				res, err := d()
				done <- svcReply(req, res, err)
			}()
			return
		}
		done <- svcReply(req, res, err)
	})
	select {
	case msg := <-done:
		if data, err := json.Marshal(msg); err == nil {
			_ = p.write(data)
		}
	case <-s.quit:
	}
}

// svcReply is the reply to a request.
func svcReply(req svcRequest, res any, err error) svcMessage {
	msg := svcMessage{ID: req.ID}
	if err != nil {
		msg.Error = err.Error()
	} else if res != nil {
		msg.Result, _ = json.Marshal(res)
	}
	return msg
}

// handle runs a window's request on the executor.
func (s *service) handle(p *svcPeer, req svcRequest) (any, error) {
	c := s.c
	arg := func(v any) error {
		if len(req.Args) == 0 {
			return nil
		}
		return json.Unmarshal(req.Args, v)
	}
	var id int
	var value string
	switch req.Op {
	case "notification-presence":
		var present bool
		if err := arg(&present); err != nil {
			return nil, err
		}
		if present && c.push != nil {
			c.push.mu.Lock()
			c.push.desktopUntil = time.Now().Add(25 * time.Second)
			c.push.mu.Unlock()
		}
	case "hello":
		return svcHello{Version: serviceVersion, Exe: s.exe, ExeTime: s.exeTime, PID: os.Getpid(), Busy: s.busy()}, nil
	case "runtimeValidate":
		var r phoneWebService
		if err := arg(&r); err != nil {
			return nil, err
		}
		manager := c.runtimes()
		return deferred(func() (any, error) {
			ctx, cancel := context.WithTimeout(manager.ctx, 5*time.Second)
			defer cancel()
			return nil, manager.validateToken(ctx, r.RuntimeToken, r.Host, r.Port)
		}), nil
	case "runtime":
		var r runtimeRequest
		if err := arg(&r); err != nil {
			return nil, err
		}
		return c.prepareRuntime(r.Ref, r.Project)
	case "state":
		return c.snapshotState(), nil
	case "desktop-read":
		var read desktopRead
		if err := arg(&read); err != nil {
			return nil, err
		}
		c.markDesktopRead(read)
		s.markDirty()
	case "subscribe":
		_ = arg(&id)
		p.sub, p.sent, p.order = id, nil, nil
		s.markDirty()
	case "mcpDesktopControl", "mcpMemory", "mcpThreads", "mcpRead", "mcpStatus", "mcpDelegate", "mcpAutomationList", "mcpAutomationCreate", "mcpAutomationUpdate", "mcpReportResult", "mcpRuntime":
		var r mcpRequest
		if err := arg(&r); err != nil {
			return nil, err
		}
		return c.mcpHandle(req.Op, r)
	case "memoryList", "memoryGet", "memorySave", "memoryForget", "memoryConfigure", "memorySessionConfigure", "memoryPromoteUser", "memoryPromotionPreview", "memoryPromoteFile":
		var r memoryRequest
		if err := arg(&r); err != nil {
			return nil, err
		}
		return c.memoryOp(req.Op, r)
	case "automationList", "automationDraft", "automationSave", "automationToggle", "automationDelete", "automationRunNow":
		var r automationRequest
		if err := arg(&r); err != nil {
			return nil, err
		}
		return c.automationOp(req.Op, r)
	case "prefetch":
		var ref ThreadRef
		if err := arg(&ref); err != nil {
			return nil, err
		}
		c.prefetchReference(ref)
	case "submit":
		var r sendRequest
		if err := arg(&r); err != nil {
			return nil, err
		}
		id := c.submit(r)
		if id == 0 && c.lastSubmitError != "" {
			return nil, fmt.Errorf("%s", c.lastSubmitError)
		}
		return id, nil
	case "retry":
		var r retryRequest
		if err := arg(&r); err != nil {
			return nil, err
		}
		return nil, c.retryResponse(r)
	case "stop":
		_ = arg(&id)
		c.stopThread(id)
	case "archive", "delete", "open", "resume":
		_ = arg(&id)
		t := c.threadWithID(id)
		if t.ID == 0 {
			return nil, nil
		}
		switch req.Op {
		case "archive":
			c.archiveThread(t)
		case "delete":
			c.deleteThread(t)
		case "open":
			backendOf(t).Open(c, t)
		case "resume":
			c.resume(t, nil)
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
		for _, ap := range c.approvals() {
			if string(ap.ID) == string(a.ID) {
				if a.Remember {
					return nil, c.respondMCPAlways(ap)
				}
				c.respond(ap, result)
				break
			}
		}
		if a.Remember {
			return nil, fmt.Errorf("请求已处理或已过期")
		}
	case "rewind":
		var a svcRewind
		_ = arg(&a)
		c.rewind(a.Thread, a.Message, a.Text)
	case "task":
		var a svcTaskAction
		if err := json.Unmarshal(req.Args, &a); err != nil {
			return nil, err
		}
		return c.taskAction(a.Thread, a.Task, a.Action), nil
	case "command":
		var a svcCommand
		_ = arg(&a)
		thread, message := c.threadCommand(a.Thread, a.Agent, a.Command, a.Policy)
		return svcCommandResult{Thread: thread, Message: message, FastMode: c.fastMode}, nil
	case "workspace":
		_ = arg(&value)
		if err := c.changeWorkspace(value); err != nil {
			return svcWorkspace{Error: err.Error()}, nil
		}
		return svcWorkspace{Cwd: c.cwd}, nil
	case "connect":
		c.connect()
	case "model", "effort", "policy", "locale", "claudeCommand":
		_ = arg(&value)
		switch req.Op {
		case "model":
			c.selectModel(value)
		case "effort":
			c.setCodexEffort(value)
		case "policy":
			c.setPermissionPolicy(value)
		case "locale":
			c.setLocale(value)
		case "claudeCommand":
			c.setClaudeCommand(value)
		}
	case "threadPolicy", "draft":
		var a svcThreadValue
		_ = arg(&a)
		if a.Thread != 0 {
			c.update(a.Thread, func(t *Thread) {
				if req.Op == "draft" {
					t.Draft = a.Value
				} else {
					t.PermissionPolicy = normalizePermissionPolicy(a.Value)
				}
			})
		}
	case "renameProject", "archiveProject", "deleteProject":
		var a svcProject
		_ = arg(&a)
		switch req.Op {
		case "renameProject":
			c.renameProject(a.Path, a.Name)
		case "archiveProject":
			c.archiveProject(a.Path)
		case "deleteProject":
			c.deleteProject(a.Path)
		}
	case "phone":
		_ = arg(&value)
		desktopPhone.do(value)
		invalidate()
	case "dequeue":
		var a svcQueued
		_ = arg(&a)
		c.removeQueued(a.Thread, a.ID)
	case "steerQueued":
		var a svcQueued
		_ = arg(&a)
		c.steerQueued(a.Thread, a.ID)
	case "drainQueue":
		_ = arg(&id)
		c.resumeQueue(id)
	case "claudeModel":
		_ = arg(&value)
		c.setClaudeModel(value)
	case "claudeEffort":
		_ = arg(&value)
		c.setClaudeEffort(value)
	case "account":
		c.refreshAccount()
	case "accountAsk":
		c.askAccountReset()
	case "accountCancel":
		c.cancelAccountReset()
	case "accountConfirm":
		c.confirmAccountReset()
	case "quit":
		s.stop()
	case "quitWhenIdle":
		s.quitWhenIdle = true
		if c.memoryEngine != nil {
			c.memoryEngine.pause()
		}
		if !s.busy() {
			s.stop()
		}
	default:
		return nil, fmt.Errorf("unknown operation %q", req.Op)
	}
	return nil, nil
}

// busy reports a turn running or starting, which quitting would end.
func (s *service) busy() bool {
	if s.c.desktopBusy() {
		return true
	}
	if s.c.runtimeBusy() {
		return true
	}
	if s.c.memoryCalls > 0 || s.c.memoryEngine != nil && s.c.memoryEngine.busy.Load() || len(s.c.claudeRuns) > 0 {
		return true
	}
	for _, t := range s.c.threads() {
		if t.Busy || t.TaskPending || activeThreadGoal(t) {
			return true
		}
	}
	for path, threads := range s.c.projectCache {
		if path == s.c.cwd {
			continue
		}
		for _, t := range threads {
			if t.Busy || t.TaskPending || activeThreadGoal(t) {
				return true
			}
		}
	}
	return false
}

func (s *service) peerList() []*svcPeer {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*svcPeer, 0, len(s.peers))
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
func (s *service) publishLoop() {
	var last time.Time
	for {
		select {
		case <-s.quit:
			return
		case <-s.dirty:
		}
		wait := publishDelay
		if gap := publishGap - time.Since(last); gap > wait {
			wait = gap
		}
		t := time.NewTimer(wait)
		select {
		case <-s.quit:
			t.Stop()
			return
		case <-t.C:
		}
		last = time.Now()
		s.exec.push(s.publish)
	}
}

func (s *service) publish() {
	st := s.c.snapshotState()
	data, err := json.Marshal(svcMessage{Event: "state", State: &st})
	if err != nil {
		return
	}
	for _, p := range s.peerList() {
		if !bytes.Equal(p.lastState, data) {
			if p.write(data) == nil {
				p.lastState = data
			}
		}
		if p.sub != 0 {
			s.publishMessages(p)
		}
	}
}

func (s *service) publishMessages(p *svcPeer) {
	t := s.c.threadWithID(p.sub)
	if t.ID == 0 {
		return
	}
	if update, ok := p.diff(t); ok {
		if data, err := json.Marshal(svcMessage{Event: "messages", Messages: &update}); err == nil {
			_ = p.write(data)
		}
	}
}

// diff is the update that brings the window's copy of t up to date, and
// whether there is one; it records the update as sent.
func (p *svcPeer) diff(t Thread) (svcMessages, bool) {
	update := svcMessages{Thread: t.ID, Reset: p.sent == nil}
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
		if fp := messageFingerprint(m); p.sent[m.ID] != fp {
			p.sent[m.ID] = fp
			update.Changed = append(update.Changed, m)
		}
	}
	if !update.Reset && same && len(update.Changed) == 0 {
		return svcMessages{}, false
	}
	p.order = update.Order
	return update, true
}

// draftReturned gives a message that could not be sent to every window;
// the one showing its conversation puts it back in its composer.
func (s *service) draftReturned(id int, text string, attachments []string) {
	data, err := json.Marshal(svcMessage{Event: "draft", Draft: &svcDraft{Thread: id, Text: text, Attachments: attachments}})
	if err != nil {
		return
	}
	for _, p := range s.peerList() {
		_ = p.write(data)
	}
}

// watchIdle ends the service once nothing needs it.
func (s *service) watchIdle(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.quit:
			return
		case <-t.C:
		}
		s.exec.push(func() {
			busy := s.busy()
			if s.quitWhenIdle && !busy {
				s.stop()
				return
			}
			// A connected phone keeps the service up, as a window does, and so
			// does an automation that is on.
			if busy || len(s.peerList()) > 0 || desktopPhone.connected() || s.c.automationsEnabled() {
				s.idleSince = time.Time{}
				return
			}
			if s.idleSince.IsZero() {
				s.idleSince = time.Now()
			} else if time.Since(s.idleSince) >= s.idleTimeout {
				s.stop()
			}
		})
	}
}

// snapshotState is the state windows show, without messages.
func (c *chatController) snapshotState() svcState {
	busy := make(map[string]bool)
	for _, p := range c.projects() {
		if c.projectBusy(p.Path) {
			busy[p.Path] = true
		}
	}
	threads := make([]Thread, len(c.threads()))
	for i, t := range c.threads() {
		t.Messages = nil
		threads[i] = t
	}
	projectThreads := make(map[string][]Thread, len(c.projectCache))
	for path, cached := range c.projectCache {
		if path == c.cwd {
			continue
		}
		items := make([]Thread, len(cached))
		for i, t := range cached {
			t = listThread(t)
			t.Queue = nil
			items[i] = t
		}
		projectThreads[path] = items
	}
	return svcState{
		ReferenceSource: c.referenceSource,
		MemoryRevision:  c.memoryRevision,
		FastMode:        c.fastMode,
		ProjectBusy:     busy,
		ProjectThreads:  projectThreads,
		DesktopUnread:   c.desktopUnread,
		Cwd:             c.cwd, Projects: c.projects(), Threads: threads, Approvals: c.approvals(),
		Status: c.status(), HistoryStatus: c.historyStatus(), Ready: c.ready(),
		Models: c.models(), Model: c.model(), Effort: c.effort(), PermissionPolicy: c.permissionPolicy(), Locale: c.locale(),
		ClaudeCommand: c.claudeCommand, ClaudeError: c.claudeError, ClaudeBinary: c.claudeBinary, ClaudeArgs: c.claudeArgs, ClaudeEnv: c.claudeEnv, ClaudeModel: c.claudeModel, ClaudeEffort: c.claudeEffort, ClaudeDefaultModel: c.claudeDefaultModel,
		Account: c.accountState, AccountReset: c.accountReset, QuotaLimits: c.quotaLimits, QuotaStatus: c.quotaStatus,
		Phone: desktopPhone.status(),
	}
}
