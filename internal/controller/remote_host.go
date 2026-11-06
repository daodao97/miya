package controller

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

	"miya/internal/agent"
	claude "miya/internal/agent/claude/native"
	"miya/internal/agent/claude/native/transcript"
	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	"miya/internal/async"
	"miya/internal/desktop"
	"miya/internal/model"
	"miya/internal/msgfmt"
	phoneproto "miya/internal/phone/protocol"
	phoneserver "miya/internal/phone/server"
	"miya/internal/remote"
	"miya/internal/thumbnail"
	"miya/internal/uiruntime"
)

// The desktop shows its state to phones; nothing a phone sends changes it.
// Each phone polls the controller on the main thread and is sent only what
// changed: the project and conversation list, and the messages of the one
// conversation it has open.
const (
	phonePollInterval = 400 * time.Millisecond
	phonePingInterval = 15 * time.Second
)

func (c *Controller) PhoneSnapshot() phoneproto.State {
	s := phoneproto.State{DesktopControl: desktop.Available(), ReferenceSource: c.ReferenceSource, MemoryRevision: c.MemoryRevision, Memory: c.Memories().Store != nil, Files: true, WebPorts: true, WebServices: true, Runtime: true, FileReferences: true, Automations: c.Auto != nil, Status: c.Status(), Approvals: c.Approvals(), Agents: c.phoneAgentOptions(), Quotas: c.PhoneQuotas()}
	s.Collaboration = c.CollaborationCapabilities()
	s.DesktopWindows = s.DesktopControl
	s.AutomationRun = c.Auto != nil
	s.AutomationCreate = c.Auto != nil
	if c.Agent != nil {
		s.Agent = c.Agent()
	}
	if c.Model != nil {
		s.Model = c.Model()
	}
	if c.Effort != nil {
		s.Effort = c.Effort()
	}
	projects := c.Projects()
	found := false
	for _, p := range projects {
		if p.Path == c.Cwd {
			found = true
		}
	}
	if !found {
		projects = append([]model.Project{{Name: filepath.Base(c.Cwd), Path: c.Cwd}}, projects...)
	}
	for _, p := range projects {
		threads := c.ProjectCache[p.Path]
		if p.Path == c.Cwd {
			threads = c.Threads()
		}
		pp := model.ProjectSnapshot{Name: p.Name, Path: p.Path, Current: p.Path == c.Cwd, Threads: make([]model.Thread, 0, len(threads))}
		for _, t := range threads {
			pp.Threads = append(pp.Threads, model.ListThread(t))
		}
		pp.Threads = msgfmt.DisplayedThreads(pp.Threads, true)
		s.Projects = append(s.Projects, pp)
	}
	return s
}

type phoneThread struct {
	Thread model.Thread
	Found  bool
	cwd    string
	client *codex.Client
}

func (c *Controller) phoneThread(id int) phoneThread {
	for _, t := range c.Threads() {
		if t.ID == id {
			return phoneThread{t, true, c.Cwd, c.Client}
		}
	}
	for path, threads := range c.ProjectCache {
		for _, t := range threads {
			if t.ID == id {
				return phoneThread{t, true, path, c.Client}
			}
		}
	}
	return phoneThread{}
}

// A stable identity must never fall back to a number reused by another thread.
func (c *Controller) PhoneThreadFor(r remote.Request) phoneThread {
	if r.ServerID == "" {
		return c.phoneThread(r.Thread)
	}
	match := func(t model.Thread, path string) bool {
		return t.ServerID == r.ServerID && (r.Agent == "" || model.ThreadAgent(t) == r.Agent) &&
			(r.Project == "" || path == r.Project)
	}
	for _, t := range c.Threads() {
		if match(t, c.Cwd) {
			return phoneThread{t, true, c.Cwd, c.Client}
		}
	}
	for path, threads := range c.ProjectCache {
		for _, t := range threads {
			if match(t, path) {
				return phoneThread{t, true, path, c.Client}
			}
		}
	}
	return phoneThread{}
}

// servePhone keeps one phone up to date until it disconnects.
func (c *Controller) ServePhone(ctx context.Context, conn *remote.Conn) {
	c.ServePhoneConnection(ctx, conn)
}

// sendPhonePreview runs only in a bounded worker. Its caller snapshots the
// subscription authorization; requests before subscribe still check history.
func (c *Controller) SendPhonePreview(ctx context.Context, conn phoneConnection, r remote.Request, allowed bool, send func(remote.Frame) bool) bool {
	img := thumbnail.Image{Source: r.ImageSource}
	var imageErr error
	if !allowed {
		// A cached message can request its image before subscribe
		// finishes. Validate against that conversation's history too.
		target, ok := async.OnMain(ctx, c.Dispatch, func() phoneThread { return c.PhoneThreadFor(r) })
		if !ok {
			return false
		}
		if target.Found {
			messages := target.Thread.Messages
			if !phoneserver.HasHistory(target.Thread) {
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

func (c *Controller) ServePhoneConnection(ctx context.Context, conn phoneConnection) {
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
				if !send(c.PhoneCollaboration(ctx, r)) {
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
					if !c.SendPhonePreview(ctx, conn, task.request, task.allowed, send) {
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
		state    phoneproto.State
		revision int
		t        phoneThread
		loading  bool
	}
	for {
		want := ps.sub
		ref := ps.subscription
		s, ok := async.OnMain(ctx, c.Dispatch, func() snapshot {
			v := snapshot{state: c.PhoneSnapshot(), revision: c.Revision(),
				loading: c.Connecting || !c.Ready() || c.HistoryStatus() == "Loading conversations…"}
			if ps.deviceID != "" && c.Push != nil {
				v.state.Notifications = c.Push.State(ps.deviceID)
			}
			if want != 0 || ref.ServerID != "" {
				v.t = c.PhoneThreadFor(ref)
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
		if s.t.Found && s.t.Thread.ID != ps.sub {
			ps.sub, ps.subscription.Thread = s.t.Thread.ID, s.t.Thread.ID
			ps.lastRevision, ps.read, ps.fetched, ps.sync = -1, nil, false, nil
		}
		if ps.sub != 0 && s.revision != ps.lastRevision {
			ps.lastRevision = s.revision
			t := s.t.Thread
			switch {
			case !s.t.Found:
				t = model.Thread{ID: ps.sub, ServerID: ps.subscription.ServerID, Agent: ps.subscription.Agent, Status: "Conversation is no longer on the desktop"}
				if s.loading {
					t.Loading, t.Status = true, "Loading conversation…"
				}
			case phoneserver.HasHistory(t) || t.Loading:
				// The desktop's own copy is live; prefer it.
			case ps.read != nil:
				t.Messages, t.HistoryLoaded, t.Status = ps.read.Messages, true, ps.read.Status
			case !ps.fetched:
				ps.fetched = true
				messages, status := readOnlyHistory(ctx, s.t)
				ps.read = &model.Thread{Messages: messages, Status: status}
				t.Messages, t.HistoryLoaded, t.Status = messages, true, status
				// Read again next poll, should the desktop change meanwhile.
				ps.lastRevision = -1
			}
			if ps.sync == nil {
				ps.sync = phoneserver.NewThreadSync(nil)
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
			res, ok := async.OnMain(ctx, c.Dispatch, func() phoneSendResult { return ps.pending.Send(c) })
			if !ok {
				return
			}
			reply := remote.Frame{Type: "sent", Thread: res.Thread, Error: res.Err}
			if res.Thread == 0 && res.Err == "" && time.Now().After(ps.pending.until) {
				reply.Error = "Desktop is not ready. Try again shortly."
			}
			if res.Thread != 0 || reply.Error != "" {
				ps.pending = nil
				if !send(reply) {
					return
				}
			}
		}
		if ps.cmdWant != "" {
			project := ps.cmdWant
			k, ok := async.OnMain(ctx, c.Dispatch, func() agent.CommandCatalog { return c.BackendFor(ps.cmdAgent).Commands(project, true) })
			if !ok {
				return
			}
			if k.Known {
				ps.cmdWant = ""
				data, _ := json.Marshal(phoneproto.Commands{Agent: ps.cmdAgent, Project: project, Commands: k.PhoneChoices})
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
func readOnlyHistory(ctx context.Context, t phoneThread) ([]model.Message, string) {
	th := t.Thread
	var messages []model.Message
	var err error
	if th.Agent == "claude-code" {
		if path := transcript.Find(t.cwd, th.ServerID); path != "" {
			messages, err = transcript.Parse(path)
		}
		if len(messages) == 0 {
			var stored model.Thread
			data, rerr := os.ReadFile(claude.HistoryPath(th.ServerID))
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
			messages = msgfmt.HistoryMessages(result.Thread)
		}
	}
	if err != nil && len(messages) == 0 {
		return nil, "Unable to read conversation: " + err.Error()
	}
	for i := range messages {
		messages[i].ID = i + 1
		if messages[i].Role == "user" {
			own, _ := model.SplitReferenceContext(messages[i].Content)
			messages[i].Parts = model.MessageParts(own)
		}
	}
	return messages, ""
}

// phoneHost is the desktop's bridge to phones, in internal/phone/server.
type phoneHost = phoneserver.Pairing

var DesktopPhone *phoneHost

// newPhoneHost restores the bridge if the user left it on, so the phone's
// saved link keeps working across desktop restarts.
func NewPhoneHost(c *Controller) *phoneHost {
	p := phoneserver.NewPairing(phoneHostOf{c})
	uiruntime.OnCleanup(p.Close)
	if phoneserver.LoadEnabled() && !servingRemote {
		p.Start()
	}
	return p
}

// phoneHostOf is a controller as the phone bridge's phoneserver.Host.
type phoneHostOf struct{ c *Controller }

func (h phoneHostOf) Serve(ctx context.Context, conn *remote.Conn) { h.c.ServePhone(ctx, conn) }

func (h phoneHostOf) Files() http.Handler { return h.c.FileServer() }

func (h phoneHostOf) Dispatch(f func()) { h.c.Dispatch(f) }

func (h phoneHostOf) RunOnMain(f func()) { uiruntime.RunOnMain(f) }

func (h phoneHostOf) Invalidate() { uiruntime.Invalidate() }

type PhoneSendReq struct {
	Req              remote.Request
	paths            []string
	until            time.Time
	OwnershipChecked bool
}

// A read-only marker describes a previous resume attempt. Recheck it once per
// user send, without sending the message until the backend grants ownership.
func (p *PhoneSendReq) Send(c *Controller) phoneSendResult {
	if !p.OwnershipChecked && c.Client != nil && c.Ready() {
		target := c.PhoneThreadFor(p.Req)
		if target.Found && target.Thread.ReadOnly && !target.Thread.Loading && !target.Thread.Busy && model.ThreadAgent(target.Thread) == appmodel.AgentCodex {
			p.OwnershipChecked = true
			c.Resume(target.Thread, nil)
			return phoneSendResult{}
		}
	}
	return c.PhoneSend(p.Req, p.paths)
}

// phoneSendResult is the conversation that took a message, or why it was
// refused; neither means it waits for the desktop to be ready.
type phoneSendResult struct {
	Thread int
	Err    string
}

// phoneSend sends a phone's message. A conversation of another project
// first makes that project the desktop's, and the message waits for its
// agent to connect; the phone retries until it is sent.
func (c *Controller) PhoneSend(r remote.Request, paths []string) phoneSendResult {
	text := strings.TrimSpace(r.Text)
	if text == "" && len(paths) == 0 {
		return phoneSendResult{Err: "Message is empty"}
	}
	project := r.Project
	if r.Thread != 0 || r.ServerID != "" {
		resolved := c.PhoneThreadFor(r)
		if !resolved.Found {
			if c.Connecting || !c.Ready() || c.HistoryStatus() == "Loading conversations…" {
				return phoneSendResult{}
			}
			return phoneSendResult{Err: "Conversation is no longer on the desktop"}
		}
		project = resolved.cwd
		if project == c.Cwd {
			t := resolved.Thread
			switch {
			case t.Loading:
				return phoneSendResult{}
			case t.ReadOnly:
				return phoneSendResult{Err: "Another Codex process is using this conversation. Read-only access."}
			}
			req, err := c.PhoneSendRequest(r, paths, model.ThreadAgent(t))
			if err != nil {
				return phoneSendResult{Err: err.Error()}
			}
			req.ThreadID = t.ID
			if req.ExplicitSettings && req.Policy != "" {
				c.Update(t.ID, func(th *model.Thread) { th.PermissionPolicy = req.Policy })
			}
			id := c.Submit(req)
			return phoneSendResult{Thread: id, Err: c.LastSubmitError}
		}
	}
	if project != "" && project != c.Cwd {
		if err := c.ChangeWorkspace(project); err != nil {
			return phoneSendResult{Err: "Unable to switch to this project: " + err.Error()}
		}
		return phoneSendResult{}
	}
	if r.Thread != 0 {
		// The project's conversations are loading; try again shortly.
		return phoneSendResult{}
	}
	agent := r.Agent
	if agent == "" && c.Agent != nil {
		agent = c.Agent()
	}
	req, err := c.PhoneSendRequest(r, paths, agent)
	if err != nil {
		return phoneSendResult{Err: err.Error()}
	}
	id := c.Submit(req)
	return phoneSendResult{Thread: id, Err: c.LastSubmitError}
}
