package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"miya/internal/async"
	"miya/internal/codex"
	"miya/internal/desktop"
	"miya/internal/model"
	"miya/internal/phone"
	"miya/internal/remote"
	"miya/internal/thumbnail"
)

// The desktop shows its state to phones; nothing a phone sends changes it.
// Each phone polls the controller on the main thread and is sent only what
// changed: the project and conversation list, and the messages of the one
// conversation it has open.
const (
	phonePollInterval = 400 * time.Millisecond
	phonePingInterval = 15 * time.Second
)

func (c *chatController) phoneSnapshot() phoneState {
	s := phoneState{DesktopControl: desktop.Available(), ReferenceSource: c.referenceSource, MemoryRevision: c.memoryRevision, Memory: c.memories().Store != nil, Files: true, WebPorts: true, WebServices: true, Runtime: true, FileReferences: true, Automations: c.auto != nil, Status: c.status(), Approvals: c.approvals(), Agents: c.phoneAgentOptions(), Quotas: c.phoneQuotas()}
	s.Collaboration = c.collaborationCapabilities()
	s.DesktopWindows = s.DesktopControl
	s.AutomationRun = c.auto != nil
	s.AutomationCreate = c.auto != nil
	if c.agent != nil {
		s.Agent = c.agent()
	}
	if c.model != nil {
		s.Model = c.model()
	}
	if c.effort != nil {
		s.Effort = c.effort()
	}
	projects := c.projects()
	found := false
	for _, p := range projects {
		if p.Path == c.cwd {
			found = true
		}
	}
	if !found {
		projects = append([]Project{{Name: filepath.Base(c.cwd), Path: c.cwd}}, projects...)
	}
	for _, p := range projects {
		threads := c.projectCache[p.Path]
		if p.Path == c.cwd {
			threads = c.threads()
		}
		pp := phoneProject{Name: p.Name, Path: p.Path, Current: p.Path == c.cwd, Threads: make([]Thread, 0, len(threads))}
		for _, t := range threads {
			pp.Threads = append(pp.Threads, listThread(t))
		}
		pp.Threads = displayedThreads(pp.Threads, true)
		s.Projects = append(s.Projects, pp)
	}
	return s
}

type phoneThread struct {
	thread Thread
	found  bool
	cwd    string
	client *codex.Client
}

func (c *chatController) phoneThread(id int) phoneThread {
	for _, t := range c.threads() {
		if t.ID == id {
			return phoneThread{t, true, c.cwd, c.client}
		}
	}
	for path, threads := range c.projectCache {
		for _, t := range threads {
			if t.ID == id {
				return phoneThread{t, true, path, c.client}
			}
		}
	}
	return phoneThread{}
}

// A stable identity must never fall back to a number reused by another thread.
func (c *chatController) phoneThreadFor(r remote.Request) phoneThread {
	if r.ServerID == "" {
		return c.phoneThread(r.Thread)
	}
	match := func(t Thread, path string) bool {
		return t.ServerID == r.ServerID && (r.Agent == "" || model.ThreadAgent(t) == r.Agent) &&
			(r.Project == "" || path == r.Project)
	}
	for _, t := range c.threads() {
		if match(t, c.cwd) {
			return phoneThread{t, true, c.cwd, c.client}
		}
	}
	for path, threads := range c.projectCache {
		for _, t := range threads {
			if match(t, path) {
				return phoneThread{t, true, path, c.client}
			}
		}
	}
	return phoneThread{}
}

// servePhone keeps one phone up to date until it disconnects.
func (c *chatController) servePhone(ctx context.Context, conn *remote.Conn) {
	c.servePhoneConnection(ctx, conn)
}

// sendPhonePreview runs only in a bounded worker. Its caller snapshots the
// subscription authorization; requests before subscribe still check history.
func (c *chatController) sendPhonePreview(ctx context.Context, conn phoneConnection, r remote.Request, allowed bool, send func(remote.Frame) bool) bool {
	img := thumbnail.Image{Source: r.ImageSource}
	var imageErr error
	if !allowed {
		// A cached message can request its image before subscribe
		// finishes. Validate against that conversation's history too.
		target, ok := async.OnMain(ctx, c.dispatch, func() phoneThread { return c.phoneThreadFor(r) })
		if !ok {
			return false
		}
		if target.found {
			messages := target.thread.Messages
			if !hasHistory(target.thread) {
				messages, _ = readOnlyHistory(ctx, target)
			}
			for _, message := range messages {
				for _, source := range message.Images {
					allowed = allowed || source == r.ImageSource
				}
			}
		}
	}
	if !allowed {
		imageErr = fmt.Errorf("Image is not in the current conversation")
	} else {
		img.Data, imageErr = thumbnail.File(r.ImageSource)
	}
	if imageErr == nil {
		if transport, ok := conn.(interface{ SendImage(string, []byte) error }); ok {
			if err := transport.SendImage(img.Source, img.Data); err == nil {
				return true
			} else {
				imageErr = err
				img.Data = nil
			}
		}
	}
	data, _ := json.Marshal(img)
	frame := remote.Frame{Type: "image", Data: data}
	if imageErr != nil {
		frame.Error = imageErr.Error()
	}
	return send(frame)
}

type phoneConnection interface {
	Send(any) error
	Receive(any) error
	CompressSends() error
}

func (c *chatController) servePhoneConnection(ctx context.Context, conn phoneConnection) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	host, _ := os.Hostname()
	var lastSent atomic.Int64
	lastSent.Store(time.Now().UnixNano())
	send := func(f remote.Frame) bool {
		if conn.Send(f) != nil {
			return false
		}
		lastSent.Store(time.Now().UnixNano())
		return true
	}
	hello := remote.Frame{Type: "hello", Version: remote.ProtocolVersion, Host: host}
	if transport, ok := conn.(interface{ AttachmentToken() string }); ok {
		hello.AttachmentToken = transport.AttachmentToken()
	}
	if !send(hello) {
		return
	}
	// Conversations are markdown and tool output, which compress well.
	if conn.CompressSends() != nil {
		return
	}
	requests := make(chan remote.Request)
	collaborationJobs := make(chan remote.Request, 8)
	collaborationDone := make(chan struct{})
	go func() {
		defer close(collaborationDone)
		for {
			select {
			case r := <-collaborationJobs:
				if !send(c.phoneCollaboration(ctx, r)) {
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() { cancel(); <-collaborationDone }()
	drafts := make(chan remote.Request, 1)
	draftDone := make(chan struct{})
	go func() {
		defer close(draftDone)
		for {
			select {
			case r := <-drafts:
				if !send(c.phoneAutomationDraft(ctx, r)) {
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() { cancel(); <-draftDone }()
	go func() {
		defer close(requests)
		defer cancel() // Revoke desktop control immediately when Receive sees EOF.
		for {
			var r remote.Request
			if conn.Receive(&r) != nil {
				return
			}
			select {
			case requests <- r:
			case <-ctx.Done():
				return
			}
		}
	}()
	previews := make(chan previewRequest, 8)
	workersDone := make(chan struct{}, 2)
	for range 2 {
		go func() {
			defer func() { workersDone <- struct{}{} }()
			for {
				select {
				case task := <-previews:
					if task.request.Op == "desktop-action" {
						c.sendPhoneDesktopAction(ctx, task.request, task.allowed, send)
						continue
					}
					if task.request.Op == "desktop-preview" {
						c.sendPhoneDesktopPreview(ctx, task.request, task.allowed, task.hint, send)
						continue
					}
					if !c.sendPhonePreview(ctx, conn, task.request, task.allowed, send) {
						cancel()
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	defer func() {
		cancel()
		for range 2 {
			<-workersDone
		}
	}()
	ticker := time.NewTicker(phonePollInterval)
	defer ticker.Stop()
	ps := &phoneSession{c: c, ctx: ctx, send: send, collaborationJobs: collaborationJobs, drafts: drafts, previews: previews, lastRevision: -1}
	type snapshot struct {
		state    phoneState
		revision int
		t        phoneThread
		loading  bool
	}
	for {
		want := ps.sub
		ref := ps.subscription
		s, ok := async.OnMain(ctx, c.dispatch, func() snapshot {
			v := snapshot{state: c.phoneSnapshot(), revision: c.revision(),
				loading: c.connecting || !c.ready() || c.historyStatus() == "Loading conversations…"}
			if ps.deviceID != "" && c.push != nil {
				v.state.Notifications = c.push.State(ps.deviceID)
			}
			if want != 0 || ref.ServerID != "" {
				v.t = c.phoneThreadFor(ref)
			}
			return v
		})
		if !ok {
			return
		}
		if data, err := json.Marshal(s.state); err == nil && !bytes.Equal(data, ps.lastState) {
			if !send(remote.Frame{Type: "state", Data: data}) {
				return
			}
			ps.lastState = data
			// A startup list can arrive without a streamed-message revision.
			ps.lastRevision = -1
			if ps.read != nil && ps.read.Status != "" {
				ps.read, ps.fetched = nil, false
			}
		}
		if s.t.found && s.t.thread.ID != ps.sub {
			ps.sub, ps.subscription.Thread = s.t.thread.ID, s.t.thread.ID
			ps.lastRevision, ps.read, ps.fetched, ps.sync = -1, nil, false, nil
		}
		if ps.sub != 0 && s.revision != ps.lastRevision {
			ps.lastRevision = s.revision
			t := s.t.thread
			switch {
			case !s.t.found:
				t = Thread{ID: ps.sub, ServerID: ps.subscription.ServerID, Agent: ps.subscription.Agent, Status: "Conversation is no longer on the desktop"}
				if s.loading {
					t.Loading, t.Status = true, "Loading conversation…"
				}
			case hasHistory(t) || t.Loading:
				// The desktop's own copy is live; prefer it.
			case ps.read != nil:
				t.Messages, t.HistoryLoaded, t.Status = ps.read.Messages, true, ps.read.Status
			case !ps.fetched:
				ps.fetched = true
				messages, status := readOnlyHistory(ctx, s.t)
				ps.read = &Thread{Messages: messages, Status: status}
				t.Messages, t.HistoryLoaded, t.Status = messages, true, status
				// Read again next poll, should the desktop change meanwhile.
				ps.lastRevision = -1
			}
			if ps.sync == nil {
				ps.sync = newPhoneThreadSync(nil)
			}
			if d, changed := ps.sync.Update(t); changed {
				if data, err := json.Marshal(d); err == nil {
					if !send(remote.Frame{Type: "thread", Thread: ps.sub, Data: data}) {
						return
					}
				}
			}
		}
		if ps.pending != nil {
			res, ok := async.OnMain(ctx, c.dispatch, func() phoneSendResult { return ps.pending.send(c) })
			if !ok {
				return
			}
			reply := remote.Frame{Type: "sent", Thread: res.thread, Error: res.err}
			if res.thread == 0 && res.err == "" && time.Now().After(ps.pending.until) {
				reply.Error = "Desktop is not ready. Try again shortly."
			}
			if res.thread != 0 || reply.Error != "" {
				ps.pending = nil
				if !send(reply) {
					return
				}
			}
		}
		if ps.cmdWant != "" {
			project := ps.cmdWant
			type known struct {
				list []composerChoice
				ok   bool
			}
			k, ok := async.OnMain(ctx, c.dispatch, func() known { l, ok := c.claudeCommandsFor(project); return known{l, ok} })
			if !ok {
				return
			}
			if k.ok {
				ps.cmdWant = ""
				data, _ := json.Marshal(phoneCommands{Project: project, Commands: k.list})
				if !send(remote.Frame{Type: "commands", Data: data}) {
					return
				}
			}
		}
		if time.Since(time.Unix(0, lastSent.Load())) > phonePingInterval {
			if !send(remote.Frame{Type: "ping"}) {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case r, ok := <-requests:
			if !ok {
				return
			}
			if h := phoneRequests[r.Op]; h != nil && !h(ps, r) {
				return
			}
		case <-ticker.C:
		}
	}
}

// readOnlyHistory reads a conversation without resuming it: thread/read for
// Codex, the session files for Claude Code. Resuming would make this desktop
// the conversation's writer, a change the phone must never cause.
func readOnlyHistory(ctx context.Context, t phoneThread) ([]Message, string) {
	th := t.thread
	var messages []Message
	var err error
	if th.Agent == "claude-code" {
		if path := findClaudeSession(t.cwd, th.ServerID); path != "" {
			messages, err = parseClaudeSession(path)
		}
		if len(messages) == 0 {
			var stored Thread
			data, rerr := os.ReadFile(claudeHistoryPath(th.ServerID))
			if rerr == nil {
				rerr = json.Unmarshal(data, &stored)
			}
			if rerr == nil {
				messages, err = stored.Messages, nil
			} else if err == nil {
				err = rerr
			}
		}
	} else if t.client == nil || th.ServerID == "" {
		return nil, "Desktop is connecting to Codex…"
	} else {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var result codex.ThreadResponse
		err = t.client.Call(cctx, "thread/read", map[string]any{"threadId": th.ServerID, "includeTurns": true}, &result)
		if err == nil {
			messages = prepareHistoryMessages(result.Thread)
		}
	}
	if err != nil && len(messages) == 0 {
		return nil, "Unable to read conversation: " + err.Error()
	}
	for i := range messages {
		messages[i].ID = i + 1
		if messages[i].Role == "user" {
			own, _ := model.SplitReferenceContext(messages[i].Content)
			messages[i].Parts = MessageParts(own)
		}
	}
	return messages, ""
}

// phoneHost is the desktop's bridge to phones, in internal/phone.
type phoneHost = phone.Pairing

var desktopPhone *phoneHost

// newPhoneHost restores the bridge if the user left it on, so the phone's
// saved link keeps working across desktop restarts.
func newPhoneHost(c *chatController) *phoneHost {
	p := phone.NewPairing(phoneHostOf{c})
	onCleanup(p.Close)
	if loadPhoneEnabled() && !servingRemote {
		p.Start()
	}
	return p
}

// phoneHostOf is a controller as the phone bridge's phone.Host.
type phoneHostOf struct{ c *chatController }

func (h phoneHostOf) Serve(ctx context.Context, conn *remote.Conn) { h.c.servePhone(ctx, conn) }
func (h phoneHostOf) Files() http.Handler                          { return h.c.fileServer() }
func (h phoneHostOf) Dispatch(f func())                            { h.c.dispatch(f) }
func (h phoneHostOf) RunOnMain(f func())                           { runOnMain(f) }
func (h phoneHostOf) Invalidate()                                  { invalidate() }

type phoneSendReq struct {
	req              remote.Request
	paths            []string
	until            time.Time
	ownershipChecked bool
}

// A read-only marker describes a previous resume attempt. Recheck it once per
// user send, without sending the message until the backend grants ownership.
func (p *phoneSendReq) send(c *chatController) phoneSendResult {
	if !p.ownershipChecked && c.client != nil && c.ready() {
		target := c.phoneThreadFor(p.req)
		if target.found && target.thread.ReadOnly && !target.thread.Loading && !target.thread.Busy && model.ThreadAgent(target.thread) == agentCodex {
			p.ownershipChecked = true
			c.resume(target.thread, nil)
			return phoneSendResult{}
		}
	}
	return c.phoneSend(p.req, p.paths)
}

// phoneSendResult is the conversation that took a message, or why it was
// refused; neither means it waits for the desktop to be ready.
type phoneSendResult struct {
	thread int
	err    string
}

// phoneSend sends a phone's message. A conversation of another project
// first makes that project the desktop's, and the message waits for its
// agent to connect; the phone retries until it is sent.
func (c *chatController) phoneSend(r remote.Request, paths []string) phoneSendResult {
	text := strings.TrimSpace(r.Text)
	if text == "" && len(paths) == 0 {
		return phoneSendResult{err: "Message is empty"}
	}
	project := r.Project
	if r.Thread != 0 || r.ServerID != "" {
		resolved := c.phoneThreadFor(r)
		if !resolved.found {
			if c.connecting || !c.ready() || c.historyStatus() == "Loading conversations…" {
				return phoneSendResult{}
			}
			return phoneSendResult{err: "Conversation is no longer on the desktop"}
		}
		project = resolved.cwd
		if project == c.cwd {
			t := resolved.thread
			switch {
			case t.Loading:
				return phoneSendResult{}
			case t.ReadOnly:
				return phoneSendResult{err: "Another Codex process is using this conversation. Read-only access."}
			}
			req, err := c.phoneSendRequest(r, paths, model.ThreadAgent(t))
			if err != nil {
				return phoneSendResult{err: err.Error()}
			}
			req.ThreadID = t.ID
			if req.ExplicitSettings && req.Policy != "" {
				c.update(t.ID, func(th *Thread) { th.PermissionPolicy = req.Policy })
			}
			id := c.submit(req)
			return phoneSendResult{thread: id, err: c.lastSubmitError}
		}
	}
	if project != "" && project != c.cwd {
		if err := c.changeWorkspace(project); err != nil {
			return phoneSendResult{err: "Unable to switch to this project: " + err.Error()}
		}
		return phoneSendResult{}
	}
	if r.Thread != 0 {
		// The project's conversations are loading; try again shortly.
		return phoneSendResult{}
	}
	agent := r.Agent
	if agent == "" && c.agent != nil {
		agent = c.agent()
	}
	req, err := c.phoneSendRequest(r, paths, agent)
	if err != nil {
		return phoneSendResult{err: err.Error()}
	}
	id := c.submit(req)
	return phoneSendResult{thread: id, err: c.lastSubmitError}
}
