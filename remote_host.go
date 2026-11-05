package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/push/apns"
	"miya/internal/appdir"
	"miya/internal/automation"
	"miya/internal/codex"
	"miya/internal/desktop"
	"miya/internal/model"
	"miya/internal/notification"
	"miya/internal/remote"
	"miya/internal/thumbnail"

	qrcode "github.com/skip2/go-qrcode"
)

// The desktop shows its state to phones; nothing a phone sends changes it.
// Each phone polls the controller on the main thread and is sent only what
// changed: the project and conversation list, and the messages of the one
// conversation it has open.
const (
	phonePollInterval = 400 * time.Millisecond
	phonePingInterval = 15 * time.Second
)

type phoneProject struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Current bool     `json:"current"`
	Threads []Thread `json:"threads"`
}

type phoneState struct {
	Collaboration    *collaborationStatus    `json:"collaboration,omitempty"`
	DesktopWindows   bool                    `json:"desktopWindows,omitempty"`
	DesktopControl   bool                    `json:"desktopControl,omitempty"`
	MemoryRevision   uint64                  `json:"memoryRevision,omitempty"`
	Memory           bool                    `json:"memory,omitempty"`
	ReferenceSource  string                  `json:"referenceSource"`
	Files            bool                    `json:"files,omitempty"`
	WebPorts         bool                    `json:"webPorts,omitempty"`
	Runtime          bool                    `json:"runtime,omitempty"`
	WebServices      bool                    `json:"webServices,omitempty"`
	FileReferences   bool                    `json:"fileReferences,omitempty"`
	Automations      bool                    `json:"automations,omitempty"`
	AutomationRun    bool                    `json:"automationRun,omitempty"`
	AutomationCreate bool                    `json:"automationCreate,omitempty"`
	Projects         []phoneProject          `json:"projects"`
	Status           string                  `json:"status"`
	Approvals        []codex.Approval        `json:"approvals"`
	Agents           []phoneAgentOptions     `json:"agents,omitempty"`
	Quotas           []phoneAgentQuota       `json:"quotas,omitempty"`
	Notifications    notification.PhoneState `json:"notifications"`
	// Agent, Model and Effort are the desktop's defaults for new messages.
	Agent  string `json:"agent"`
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

// listThread is a conversation as the list shows it: no messages, and never
// the desktop's unsent draft.
func listThread(t Thread) Thread {
	t.Messages = nil
	t.Draft = ""
	return t
}

func (c *chatController) phoneSnapshot() phoneState {
	s := phoneState{DesktopControl: desktop.Available(), ReferenceSource: c.referenceSource, MemoryRevision: c.memoryRevision, Memory: c.memory != nil, Files: true, WebPorts: true, WebServices: true, Runtime: true, FileReferences: true, Automations: c.auto != nil, Status: c.status(), Approvals: c.approvals(), Agents: c.phoneAgentOptions(), Quotas: c.phoneQuotas()}
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

func hasHistory(t Thread) bool { return len(t.Messages) > 0 || t.HistoryLoaded }

// onMain runs f on the UI thread and waits for its result.
func onMain[T any](ctx context.Context, dispatch func(func()), f func() T) (T, bool) {
	ch := make(chan T, 1)
	dispatch(func() { ch <- f() })
	select {
	case v := <-ch:
		return v, true
	case <-ctx.Done():
		var zero T
		return zero, false
	}
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
		target, ok := onMain(ctx, c.dispatch, func() phoneThread { return c.phoneThreadFor(r) })
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
	type previewRequest struct {
		request remote.Request
		allowed bool
		hint    desktopWindowHint
	}
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
	var (
		deviceID  string
		lastState []byte
		// sync is what the phone holds of the conversation it watches.
		sync         *phoneThreadSync
		sub          int
		subscription remote.Request
		lastRevision = -1
		// read holds history read for the phone alone, for a conversation
		// the desktop has not opened; fetched marks the attempt.
		read    *Thread
		fetched bool
		// pending is a send waiting for the desktop to be ready for it.
		pending *phoneSendReq
		// cmdWant is a project whose Claude Code commands the phone awaits.
		cmdWant string
	)
	type snapshot struct {
		state    phoneState
		revision int
		t        phoneThread
		loading  bool
	}
	for {
		want := sub
		ref := subscription
		s, ok := onMain(ctx, c.dispatch, func() snapshot {
			v := snapshot{state: c.phoneSnapshot(), revision: c.revision(),
				loading: c.connecting || !c.ready() || c.historyStatus() == "Loading conversations…"}
			if deviceID != "" && c.push != nil {
				v.state.Notifications = c.push.State(deviceID)
			}
			if want != 0 || ref.ServerID != "" {
				v.t = c.phoneThreadFor(ref)
			}
			return v
		})
		if !ok {
			return
		}
		if data, err := json.Marshal(s.state); err == nil && !bytes.Equal(data, lastState) {
			if !send(remote.Frame{Type: "state", Data: data}) {
				return
			}
			lastState = data
			// A startup list can arrive without a streamed-message revision.
			lastRevision = -1
			if read != nil && read.Status != "" {
				read, fetched = nil, false
			}
		}
		if s.t.found && s.t.thread.ID != sub {
			sub, subscription.Thread = s.t.thread.ID, s.t.thread.ID
			lastRevision, read, fetched, sync = -1, nil, false, nil
		}
		if sub != 0 && s.revision != lastRevision {
			lastRevision = s.revision
			t := s.t.thread
			switch {
			case !s.t.found:
				t = Thread{ID: sub, ServerID: subscription.ServerID, Agent: subscription.Agent, Status: "Conversation is no longer on the desktop"}
				if s.loading {
					t.Loading, t.Status = true, "Loading conversation…"
				}
			case hasHistory(t) || t.Loading:
				// The desktop's own copy is live; prefer it.
			case read != nil:
				t.Messages, t.HistoryLoaded, t.Status = read.Messages, true, read.Status
			case !fetched:
				fetched = true
				messages, status := readOnlyHistory(ctx, s.t)
				read = &Thread{Messages: messages, Status: status}
				t.Messages, t.HistoryLoaded, t.Status = messages, true, status
				// Read again next poll, should the desktop change meanwhile.
				lastRevision = -1
			}
			if sync == nil {
				sync = newPhoneThreadSync(nil)
			}
			if d, changed := sync.update(t); changed {
				if data, err := json.Marshal(d); err == nil {
					if !send(remote.Frame{Type: "thread", Thread: sub, Data: data}) {
						return
					}
				}
			}
		}
		if pending != nil {
			res, ok := onMain(ctx, c.dispatch, func() phoneSendResult { return pending.send(c) })
			if !ok {
				return
			}
			reply := remote.Frame{Type: "sent", Thread: res.thread, Error: res.err}
			if res.thread == 0 && res.err == "" && time.Now().After(pending.until) {
				reply.Error = "Desktop is not ready. Try again shortly."
			}
			if res.thread != 0 || reply.Error != "" {
				pending = nil
				if !send(reply) {
					return
				}
			}
		}
		if cmdWant != "" {
			project := cmdWant
			type known struct {
				list []composerChoice
				ok   bool
			}
			k, ok := onMain(ctx, c.dispatch, func() known { l, ok := c.claudeCommandsFor(project); return known{l, ok} })
			if !ok {
				return
			}
			if k.ok {
				cmdWant = ""
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
			switch r.Op {
			case "desktop-preview", "desktop-action":
				allowed, ok := onMain(ctx, c.dispatch, func() bool { return c.phoneDesktopApproval(r) })
				if !ok {
					return
				}
				hint, ok := onMain(ctx, c.dispatch, func() desktopWindowHint { return c.phoneDesktopHint(r) })
				if !ok {
					return
				}
				select {
				case previews <- previewRequest{request: r, allowed: allowed, hint: hint}:
				default:
					data, _ := json.Marshal(phoneDesktopPreview{RequestID: r.RequestID, Approval: r.Approval})
					if !send(remote.Frame{Type: r.Op, Data: data, Error: "The permission window is busy. Try again later."}) {
						return
					}
				}
			case "desktop-open":
				target, ok := onMain(ctx, c.dispatch, func() phoneThread { return c.phoneThreadFor(r) })
				if !ok {
					return
				}
				reply := phoneDesktopReply{RequestID: r.RequestID}
				var err error
				if !desktop.Available() {
					err = fmt.Errorf("Remote desktop requires macOS 14 or later")
				} else if !target.found || r.ServerID == "" {
					err = fmt.Errorf("Conversation is no longer on the desktop. Reopen it.")
				} else {
					var window *desktop.Window
					if r.WindowOnly {
						window = &desktop.Window{ID: r.WindowID, PID: r.WindowPID}
						allowed, alive := onMain(ctx, c.dispatch, func() bool { return c.phoneDesktopApproval(r) })
						if !alive {
							return
						}
						if !allowed {
							err = fmt.Errorf("Permission request handled. Return to the conversation.")
						}
					} else if r.WindowPick {
						// Choosing a window shares no more than the full desktop the
						// person opened from the menu, so it needs no approval.
						window = &desktop.Window{}
					}
					if err == nil {
						reply.Token, err = c.desktopControls().openWindow(ctx, window)
						if err == nil && r.WindowPick && !r.WindowOnly {
							c.desktopControls().allowPick(reply.Token)
						}
						if err == nil && r.WindowOnly {
							d := c.desktopControls()
							d.mu.Lock()
							if l := d.valid(reply.Token); l != nil {
								l.onAction = func(window desktop.Window, action desktop.Action, before []desktop.Window) {
									actionRequest := r
									actionRequest.WindowID, actionRequest.WindowPID = window.ID, window.PID
									c.confirmDesktopAction(ctx, actionRequest, action, before)
								}
							}
							d.mu.Unlock()
						}
					}
				}
				data, _ := json.Marshal(reply)
				frame := remote.Frame{Type: "desktop-open", Data: data}
				if err != nil {
					frame.Error = err.Error()
				}
				if !send(frame) {
					return
				}
			case "desktop-close":
				c.desktopControls().revoke(r.DesktopToken)
			case "image":
				allowed := sync != nil && r.Thread == sub && (r.ServerID == "" || r.ServerID == subscription.ServerID) && sync.images[r.ImageSource]
				select {
				case previews <- previewRequest{request: r, allowed: allowed}:
				default:
					data, _ := json.Marshal(thumbnail.Image{Source: r.ImageSource})
					if !send(remote.Frame{Type: "image", Data: data, Error: "Image preview is busy. Try again shortly."}) {
						return
					}
				}
			case "memory":
				// An advertised, read-only capability. No mobile mutation route.
				type pendingMemory struct {
					revision uint64
					result   any
					err      error
				}
				pending, ok := onMain(ctx, c.dispatch, func() pendingMemory {
					v, err := c.memoryOp("memoryList", memoryRequest{Project: r.Project, Scope: r.MemoryScope})
					return pendingMemory{revision: c.memoryRevision, result: v, err: err}
				})
				if !ok {
					return
				}
				result, err := pending.result, pending.err
				if d, ok := result.(deferred); ok && err == nil {
					result, err = d()
				}
				reply := phoneMemoryResult{Revision: pending.revision, Project: r.Project, Scope: r.MemoryScope}
				frame := remote.Frame{Type: "memory"}
				if err != nil {
					frame.Error = err.Error()
				} else if list, ok := result.(memoryList); ok {
					reply.List = list
				}
				frame.Data, _ = json.Marshal(reply)
				if !send(frame) {
					return
				}
			case "collaboration":
				select {
				case collaborationJobs <- r:
				default:
					if !send(collaborationPhoneFrame(r.RequestID, nil, fmt.Errorf("Collaboration requests are busy. Retry with the same request_id."))) {
						return
					}
				}
			case "automation-draft":
				select {
				case drafts <- r:
				default:
					if !send(phoneAutomationFormFrame(r, nil, fmt.Errorf("Another automation is being generated. Try again shortly."))) {
						return
					}
				}
			case "automation-create":
				frame, ok := onMain(ctx, c.dispatch, func() remote.Frame { return c.phoneAutomationCreate(r) })
				if !ok || !send(frame) {
					return
				}
			case "automation-run":
				frame, ok := onMain(ctx, c.dispatch, func() remote.Frame {
					_, err := c.automationOp("automationRunNow", automation.Request{ID: r.AutomationID})
					result := phoneAutomationRunResult{RequestID: r.RequestID}
					list, _ := c.automationOp("automationList", automation.Request{})
					result.List, _ = list.([]automation.View)
					data, _ := json.Marshal(result)
					frame := remote.Frame{Type: "automation-run", Data: data}
					if err != nil {
						frame.Error = err.Error()
					}
					return frame
				})
				if !ok || !send(frame) {
					return
				}
			case "automations":
				frame, ok := onMain(ctx, c.dispatch, func() remote.Frame {
					result, err := c.automationOp("automationList", automation.Request{})
					frame := remote.Frame{Type: "automations"}
					if err == nil {
						frame.Data, err = json.Marshal(result)
					}
					if err != nil {
						frame.Error = err.Error()
					}
					return frame
				})
				if !ok || !send(frame) {
					return
				}
			case "quota":
				if _, ok := onMain(ctx, c.dispatch, func() bool { c.refreshQuota(); return true }); !ok {
					return
				}
			case "push":
				var registration notification.Registration
				var message string
				if json.Unmarshal(r.Push, &registration) != nil {
					message = "Invalid notification registration"
				} else {
					result, _ := onMain(ctx, c.dispatch, func() string {
						if c.push == nil {
							return "Notification service unavailable"
						}
						if err := c.push.Register(registration); err != nil {
							return err.Error()
						}
						return ""
					})
					message = result
					if message == "" {
						deviceID = registration.ID
					}
					if message == "" && !registration.Disabled {
						if _, err := apns.OpenProvider(appdir.Dir()); err != nil {
							message = "Phone registered. APNs is not configured on the desktop; run mygo push setup."
						}
					}
				}
				if !send(remote.Frame{Type: "push", Error: message}) {
					return
				}
			case "read", "read-all":
				n, _ := onMain(ctx, c.dispatch, func() notification.PhoneState {
					if r.Op == "read" {
						c.markPhoneCompletionRead(r)
					}
					if c.push == nil || deviceID == "" {
						return notification.PhoneState{}
					}
					if r.Op == "read-all" {
						return c.push.MarkAllRead(deviceID, r.ReadVersion)
					}
					target := c.phoneThreadFor(r)
					if !target.found {
						return c.push.State(deviceID)
					}
					notice := notification.Notice{Thread: fmt.Sprint(target.thread.ID), ServerID: target.thread.ServerID, Agent: model.ThreadAgent(target.thread), Project: target.cwd}
					return c.push.MarkRead(deviceID, notice.UnreadKey(), r.ReadVersion)
				})
				data, _ := json.Marshal(n)
				if !send(remote.Frame{Type: "read", Data: data}) {
					return
				}
			case "subscribe":
				// Retry the same thread too: an earlier history read may have failed
				// while the desktop was still reconnecting.
				subscription = r
				sub, lastRevision, read, fetched = r.Thread, -1, nil, false
				sync = newPhoneThreadSync(r.Have)
			case "send":
				if pending != nil {
					if !send(remote.Frame{Type: "sent", Error: "The previous message is still being sent"}) {
						return
					}
				} else {
					paths, err := savePhoneImages(r.Images)
					r.Images = nil
					if err != nil {
						if !send(remote.Frame{Type: "sent", Error: err.Error()}) {
							return
						}
					} else {
						pending = &phoneSendReq{req: r, paths: paths, until: time.Now().Add(30 * time.Second)}
					}
				}
			case "prefetch":
				onMain(ctx, c.dispatch, func() bool {
					if ref, ok := parseThreadURL(r.Reference); ok {
						c.prefetchReference(ref)
					}
					return true
				})
			case "stop":
				onMain(ctx, c.dispatch, func() bool {
					if t := c.phoneThreadFor(r); t.found {
						c.stopThread(t.thread.ID)
					}
					return true
				})
			case "task":
				message, _ := onMain(ctx, c.dispatch, func() string {
					t := c.phoneThreadFor(r)
					if !t.found {
						return "Conversation is no longer on the desktop"
					}
					id, action, _ := strings.Cut(r.Text, " ")
					return c.taskAction(t.thread.ID, id, action)
				})
				if !send(remote.Frame{Type: "action", Thread: r.Thread, Error: message}) {
					return
				}
			case "retry":
				message, _ := onMain(ctx, c.dispatch, func() string {
					t := c.phoneThreadFor(r)
					if !t.found {
						return "Conversation is no longer on the desktop"
					}
					if err := c.retryResponse(retryRequest{ThreadID: t.thread.ID, MessageID: r.MessageID}); err != nil {
						return err.Error()
					}
					return ""
				})
				if !send(remote.Frame{Type: "action", Thread: r.Thread, Error: message}) {
					return
				}
			case "dequeue", "steer-queued", "drain-queue":
				message, _ := onMain(ctx, c.dispatch, func() string {
					if err := c.phoneQueueAction(r); err != nil {
						return err.Error()
					}
					return ""
				})
				if !send(remote.Frame{Type: "action", Thread: r.Thread, Error: message}) {
					return
				}
			case "respond":
				message, _ := onMain(ctx, c.dispatch, func() string {
					if err := c.phoneApprovalResponse(r); err != nil {
						return err.Error()
					}
					return ""
				})
				data, _ := json.Marshal(phoneApprovalReply{ID: r.Approval})
				if !send(remote.Frame{Type: "responded", Data: data, Error: message}) {
					return
				}
			case "commands":
				cmdWant = r.Project
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
			own, _ := splitReferenceContext(messages[i].Content)
			messages[i].Parts = messageParts(own)
		}
	}
	return messages, ""
}

// phoneHost owns the desktop's bridge. Its fields belong to the main thread.
type phoneHost struct {
	service    *service
	c          *chatController
	bridge     *remote.Bridge
	link       string
	qr         [][]bool
	peers      int
	busy       bool
	enabled    bool
	message    string
	generation int
}

var desktopPhone *phoneHost

func phoneDir() string {
	if os.Getenv("MIYA_DIR") != "" || appdir.Isolated() {
		return filepath.Join(appdir.Dir(), "remote")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "quickgui-remote")
}

func phonePrefsPath() string {
	if dir := phoneDir(); dir != "" {
		return filepath.Join(dir, "phone.json")
	}
	return ""
}

func loadPhoneEnabled() bool {
	var prefs struct {
		Enabled bool `json:"enabled"`
	}
	data, err := os.ReadFile(phonePrefsPath())
	return err == nil && json.Unmarshal(data, &prefs) == nil && prefs.Enabled
}

func persistPhoneEnabled(enabled bool) {
	path := phonePrefsPath()
	if path == "" || os.MkdirAll(filepath.Dir(path), 0700) != nil {
		return
	}
	data, _ := json.Marshal(map[string]bool{"enabled": enabled})
	_ = os.WriteFile(path, data, 0600)
}

// newPhoneHost restores the bridge if the user left it on, so the phone's
// saved link keeps working across desktop restarts.
func newPhoneHost(c *chatController) *phoneHost {
	p := &phoneHost{c: c}
	onCleanup(func() {
		p.generation++
		if b := p.bridge; b != nil {
			p.bridge = nil
			b.Close()
		}
	})
	if loadPhoneEnabled() && !servingRemote {
		p.start()
	}
	return p
}

func (p *phoneHost) start() {
	if p.bridge != nil || p.busy {
		return
	}
	dir := phoneDir()
	if dir == "" {
		p.message = "Unable to determine data directory"
		return
	}
	p.enabled = true
	persistPhoneEnabled(true)
	p.busy, p.message = true, ""
	p.generation++
	gen := p.generation
	c := p.c
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		options := remote.Options{
			StateDir: dir,
			Serve:    c.servePhone,
			Files:    c.fileServer(),
			OnPeers: func(n int) {
				runOnMain(func() {
					if p.generation == gen {
						p.peers = n
						invalidate()
					}
				})
			},
		}
		if p.service != nil {
			options.Service = func(ctx context.Context, conn *remote.Conn) { p.service.handleConn(&svcPeer{wire: conn}) }
			options.Terminal = c.serveRemoteTerminal
		}
		b, err := remote.Start(ctx, options)
		var link string
		var qr [][]bool
		if err == nil {
			link = b.Link()
			if code, qerr := qrcode.New(link, qrcode.Medium); qerr == nil {
				qr = code.Bitmap()
			}
		}
		runOnMain(func() {
			if p.generation != gen {
				if b != nil {
					go b.Close()
				}
				return
			}
			p.busy = false
			if err != nil {
				p.message = "Unable to enable mobile connections: " + err.Error()
				if p.service != nil {
					log.Printf("miya: Tailcat startup failed: %v; retrying", err)
					time.AfterFunc(5*time.Second, func() {
						c.dispatch(func() {
							if p.enabled && p.generation == gen {
								p.start()
							}
						})
					})
				}
			} else {
				p.bridge, p.link, p.qr = b, link, qr
				if p.service != nil {
					if err := saveServerConnection(link); err != nil {
						p.message = err.Error()
						log.Printf("miya: cannot save pairing link: %v", err)
					} else {
						log.Printf("miya: Tailcat ready; pairing link saved in %s", connectionFile())
					}
				}
			}
			invalidate()
		})
	}()
}

// stop disconnects phones and stays off at the next launch. The identity is
// kept, so turning it on again shows the same QR code.
func (p *phoneHost) stop() {
	p.generation++
	p.enabled = false
	persistPhoneEnabled(false)
	if b := p.bridge; b != nil {
		go b.Close()
	}
	p.bridge, p.link, p.qr, p.peers, p.busy, p.message = nil, "", nil, 0, false, ""
}

// reset revokes the current QR code: phones holding it can no longer
// connect, and a new one is shown.
func (p *phoneHost) reset() {
	if p.busy {
		return
	}
	p.generation++
	b := p.bridge
	p.bridge, p.link, p.qr, p.peers, p.busy, p.message = nil, "", nil, 0, true, ""
	dir := phoneDir()
	go func() {
		if b != nil {
			b.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		err := remote.ForgetIdentity(ctx, dir)
		runOnMain(func() {
			p.busy = false
			if err != nil {
				p.message = "Unable to reset connection: " + err.Error()
				invalidate()
				return
			}
			p.start()
		})
	}()
}

// phoneStatus is what the settings show of the phone connection; the
// service sends it to its windows.
type phoneStatus struct {
	Enabled bool   `json:"enabled,omitempty"`
	Busy    bool   `json:"busy,omitempty"`
	Link    string `json:"link,omitempty"`
	Peers   int    `json:"peers,omitempty"`
	Message string `json:"message,omitempty"`
}

func (p *phoneHost) status() phoneStatus {
	if p == nil {
		return phoneStatus{}
	}
	s := phoneStatus{Enabled: p.enabled, Busy: p.busy, Peers: p.peers, Message: p.message}
	if p.bridge != nil {
		s.Link = p.link
	}
	return s
}

func (p *phoneHost) connected() bool { return p != nil && p.peers > 0 }

// do runs a settings action: start, stop or reset.
func (p *phoneHost) do(action string) {
	if p == nil {
		return
	}
	switch action {
	case "start":
		p.start()
	case "stop":
		p.stop()
	case "reset":
		p.reset()
	}
}

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

// sameJSON compares two JSON values regardless of their spacing.
func sameJSON(a, b json.RawMessage) bool {
	var x, y bytes.Buffer
	if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
		return string(a) == string(b)
	}
	return x.String() == y.String()
}

// phoneThreadDelta brings the phone's copy of a conversation up to date:
// its metadata, the order of all its messages, and the messages the phone
// does not have as they are now. Reset drops what the phone had first.
type phoneThreadDelta struct {
	Thread  Thread    `json:"thread"`
	Order   []int     `json:"order"`
	Changed []Message `json:"changed,omitempty"`
	Reset   bool      `json:"reset,omitempty"`
}

// phoneThreadSync is what one phone holds of the conversation it watches,
// as the service's windows have theirs (svcPeer.diff).
type phoneThreadSync struct {
	images map[string]bool
	sent   map[int]uint64
	order  []int
	meta   []byte
	fresh  bool
}

// newPhoneThreadSync starts from what the phone said it holds; without it
// the first update replaces the phone's copy.
func newPhoneThreadSync(have []remote.Seen) *phoneThreadSync {
	s := &phoneThreadSync{sent: map[int]uint64{}, fresh: len(have) == 0}
	for _, h := range have {
		s.sent[h.ID] = h.FP
		s.order = append(s.order, h.ID)
	}
	return s
}

// update is the delta that brings the phone to t, and whether there is one;
// it records the delta as sent.
func (s *phoneThreadSync) update(t Thread) (phoneThreadDelta, bool) {
	msgs := t.Messages
	t.Messages, t.Draft = nil, ""
	meta, _ := json.Marshal(t)
	d := phoneThreadDelta{Thread: t, Reset: s.fresh, Order: make([]int, len(msgs))}
	if t.Loading && len(msgs) == 0 {
		// Resuming for a send can temporarily expose only thread metadata.
		// Keep the phone's history until the loaded messages replace it.
		d.Order = append([]int{}, s.order...)
		if !s.fresh && bytes.Equal(meta, s.meta) {
			return phoneThreadDelta{}, false
		}
		s.fresh, s.meta = false, meta
		return d, true
	}
	s.images = make(map[string]bool)
	for _, m := range msgs {
		for _, source := range m.Images {
			s.images[source] = true
		}
	}
	same := !s.fresh && len(msgs) == len(s.order)
	for i, m := range msgs {
		d.Order[i] = m.ID
		if same && s.order[i] != m.ID {
			same = false
		}
		if fp := messageFingerprint(m); s.sent[m.ID] != fp {
			s.sent[m.ID] = fp
			d.Changed = append(d.Changed, m)
		}
	}
	if same && len(d.Changed) == 0 && bytes.Equal(meta, s.meta) {
		return phoneThreadDelta{}, false
	}
	s.fresh, s.order, s.meta = false, d.Order, meta
	return d, true
}
