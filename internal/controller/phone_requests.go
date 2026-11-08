package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/push/apns"

	agentmeta "miya/internal/agent/catalog"
	"miya/internal/appdir"
	"miya/internal/appmodel"
	"miya/internal/async"
	"miya/internal/automation"
	"miya/internal/chat/model"
	"miya/internal/desktop"
	"miya/internal/desktop/control"
	"miya/internal/memory/manager"
	"miya/internal/notification"
	phoneproto "miya/internal/phone/protocol"
	phoneserver "miya/internal/phone/server"
	"miya/internal/remote"
	"miya/internal/thumbnail"
)

// phoneSession is one phone connection's state, shared by servePhoneConnection
// and the handlers of its requests. Its goroutine and the main-thread work it
// waits for through async.OnMain use it, never at the same time.
type phoneSession struct {
	c                 *Controller
	ctx               context.Context
	send              func(f remote.Frame) bool
	collaborationJobs chan remote.Request
	drafts            chan remote.Request
	previews          chan previewRequest
	deviceID          string
	lastState         []byte
	sync              *phoneserver.ThreadSync
	sub               int
	subscription      remote.Request
	lastRevision      int
	read              *model.Thread
	fetched           bool
	pending           *PhoneSendReq
	cmdWant           string
	cmdAgent          string
}

type previewRequest struct {
	request remote.Request
	allowed bool
	hint    control.WindowHint
}

// phoneRequests answer a phone's requests by their Op; unknown ones are ignored.
var phoneRequests = map[string]func(*phoneSession, remote.Request) bool{
	"desktop-preview":   (*phoneSession).onDesktopPreview,
	"desktop-action":    (*phoneSession).onDesktopPreview,
	"desktop-open":      (*phoneSession).onDesktopOpen,
	"desktop-close":     (*phoneSession).onDesktopClose,
	"image":             (*phoneSession).onImage,
	"memory":            (*phoneSession).onMemory,
	"collaboration":     (*phoneSession).onCollaboration,
	"automation-draft":  (*phoneSession).onAutomationDraft,
	"automation-create": (*phoneSession).onAutomationCreate,
	"automation-run":    (*phoneSession).onAutomationRun,
	"automations":       (*phoneSession).onAutomations,
	"quota":             (*phoneSession).onQuota,
	"push":              (*phoneSession).onPush,
	"read":              (*phoneSession).onRead,
	"read-all":          (*phoneSession).onRead,
	"subscribe":         (*phoneSession).onSubscribe,
	"send":              (*phoneSession).onSend,
	"prefetch":          (*phoneSession).onPrefetch,
	"stop":              (*phoneSession).onStop,
	"task":              (*phoneSession).onTask,
	"retry":             (*phoneSession).onRetry,
	"dequeue":           (*phoneSession).onQueueAction,
	"steer-queued":      (*phoneSession).onQueueAction,
	"drain-queue":       (*phoneSession).onQueueAction,
	"respond":           (*phoneSession).onRespond,
	"commands":          (*phoneSession).onCommands,
}

// onDesktopPreview answers a phone's "desktop-preview", "desktop-action" request; false ends the connection.
func (ps *phoneSession) onDesktopPreview(r remote.Request) bool {
	allowed, ok := async.OnMain(ps.ctx, ps.c.Dispatch, func() bool { return ps.c.PhoneDesktopApproval(r) })
	if !ok {
		return false
	}
	hint, ok := async.OnMain(ps.ctx, ps.c.Dispatch, func() control.WindowHint { return ps.c.phoneDesktopHint(r) })
	if !ok {
		return false
	}
	select {
	case ps.previews <- previewRequest{request: r, allowed: allowed, hint: hint}:
	default:
		data, _ := json.Marshal(control.Preview{RequestID: r.RequestID, Approval: r.Approval})
		if !ps.send(remote.Frame{Type: r.Op, Data: data, Error: "The permission window is busy. Try again later."}) {
			return false
		}
	}
	return true
}

// onDesktopOpen answers a phone's "desktop-open" request; false ends the connection.
func (ps *phoneSession) onDesktopOpen(r remote.Request) bool {
	target, ok := async.OnMain(ps.ctx, ps.c.Dispatch, func() phoneThread { return ps.c.PhoneThreadFor(r) })
	if !ok {
		return false
	}
	reply := phoneproto.DesktopReply{RequestID: r.RequestID}
	var err error
	if !desktop.Available() {
		err = fmt.Errorf("Remote desktop requires macOS 14 or later")
	} else if !target.Found || r.ServerID == "" {
		err = fmt.Errorf("Conversation is no longer on the desktop. Reopen it.")
	} else {
		var window *desktop.Window
		if r.WindowOnly {
			window = &desktop.Window{ID: r.WindowID, PID: r.WindowPID}
			allowed, alive := async.OnMain(ps.ctx, ps.c.Dispatch, func() bool { return ps.c.PhoneDesktopApproval(r) })
			if !alive {
				return false
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
			reply.Token, err = ps.c.desktopControls().OpenWindow(ps.ctx, window)
			if err == nil && r.WindowPick && !r.WindowOnly {
				ps.c.desktopControls().AllowPick(reply.Token)
			}
			if err == nil && r.WindowOnly {
				ps.c.desktopControls().SetOnAction(reply.Token, func(window desktop.Window, action desktop.Action, before []desktop.Window) {
					actionRequest := r
					actionRequest.WindowID, actionRequest.WindowPID = window.ID, window.PID
					ps.c.ConfirmDesktopAction(ps.ctx, actionRequest, action, before)
				})
			}
		}
	}
	data, _ := json.Marshal(reply)
	frame := remote.Frame{Type: "desktop-open", Data: data}
	if err != nil {
		frame.Error = err.Error()
	}
	if !ps.send(frame) {
		return false
	}
	return true
}

// onDesktopClose answers a phone's "desktop-close" request; false ends the connection.
func (ps *phoneSession) onDesktopClose(r remote.Request) bool {
	ps.c.desktopControls().Revoke(r.DesktopToken)
	return true
}

// onImage answers a phone's "image" request; false ends the connection.
func (ps *phoneSession) onImage(r remote.Request) bool {
	allowed := ps.sync != nil && r.Thread == ps.sub && (r.ServerID == "" || r.ServerID == ps.subscription.ServerID) && ps.sync.HasImage(r.ImageSource)
	select {
	case ps.previews <- previewRequest{request: r, allowed: allowed}:
	default:
		data, _ := json.Marshal(thumbnail.Image{Source: r.ImageSource})
		if !ps.send(remote.Frame{Type: "image", Data: data, Error: "Image preview is busy. Try again shortly."}) {
			return false
		}
	}
	return true
}

// onMemory answers a phone's "memory" request; false ends the connection.
func (ps *phoneSession) onMemory(r remote.Request) bool {
	// An advertised, read-only capability. No mobile mutation route.
	type pendingMemory struct {
		revision uint64
		result   any
		err      error
	}
	pending, ok := async.OnMain(ps.ctx, ps.c.Dispatch, func() pendingMemory {
		v, err := ps.c.Memories().Op("memoryList", manager.Request{Project: r.Project, Scope: r.MemoryScope})
		return pendingMemory{revision: ps.c.MemoryRevision, result: v, err: err}
	})
	if !ok {
		return false
	}
	result, err := pending.result, pending.err
	if d, ok := result.(async.Reply); ok && err == nil {
		result, err = d()
	}
	reply := phoneproto.MemoryResult{Revision: pending.revision, Project: r.Project, Scope: r.MemoryScope}
	frame := remote.Frame{Type: "memory"}
	if err != nil {
		frame.Error = err.Error()
	} else if list, ok := result.(manager.List); ok {
		reply.List = list
	}
	frame.Data, _ = json.Marshal(reply)
	if !ps.send(frame) {
		return false
	}
	return true
}

// onCollaboration answers a phone's "collaboration" request; false ends the connection.
func (ps *phoneSession) onCollaboration(r remote.Request) bool {
	select {
	case ps.collaborationJobs <- r:
	default:
		if !ps.send(collaborationPhoneFrame(r.RequestID, nil, fmt.Errorf("Collaboration requests are busy. Retry with the same request_id."))) {
			return false
		}
	}
	return true
}

// onAutomationDraft answers a phone's "automation-draft" request; false ends the connection.
func (ps *phoneSession) onAutomationDraft(r remote.Request) bool {
	select {
	case ps.drafts <- r:
	default:
		if !ps.send(phoneAutomationFormFrame(r, nil, fmt.Errorf("Another automation is being generated. Try again shortly."))) {
			return false
		}
	}
	return true
}

// onAutomationCreate answers a phone's "automation-create" request; false ends the connection.
func (ps *phoneSession) onAutomationCreate(r remote.Request) bool {
	frame, ok := async.OnMain(ps.ctx, ps.c.Dispatch, func() remote.Frame { return ps.c.PhoneAutomationCreate(r) })
	if !ok || !ps.send(frame) {
		return false
	}
	return true
}

// onAutomationRun answers a phone's "automation-run" request; false ends the connection.
func (ps *phoneSession) onAutomationRun(r remote.Request) bool {
	frame, ok := async.OnMain(ps.ctx, ps.c.Dispatch, func() remote.Frame {
		_, err := ps.c.AutomationOp("automationRunNow", automation.Request{ID: r.AutomationID})
		result := phoneproto.AutomationRunResult{RequestID: r.RequestID}
		list, _ := ps.c.AutomationOp("automationList", automation.Request{})
		result.List, _ = list.([]automation.View)
		data, _ := json.Marshal(result)
		frame := remote.Frame{Type: "automation-run", Data: data}
		if err != nil {
			frame.Error = err.Error()
		}
		return frame
	})
	if !ok || !ps.send(frame) {
		return false
	}
	return true
}

// onAutomations answers a phone's "automations" request; false ends the connection.
func (ps *phoneSession) onAutomations(r remote.Request) bool {
	frame, ok := async.OnMain(ps.ctx, ps.c.Dispatch, func() remote.Frame {
		result, err := ps.c.AutomationOp("automationList", automation.Request{})
		frame := remote.Frame{Type: "automations"}
		if err == nil {
			frame.Data, err = json.Marshal(result)
		}
		if err != nil {
			frame.Error = err.Error()
		}
		return frame
	})
	if !ok || !ps.send(frame) {
		return false
	}
	return true
}

// onQuota answers a phone's "quota" request; false ends the connection.
func (ps *phoneSession) onQuota(r remote.Request) bool {
	if _, ok := async.OnMain(ps.ctx, ps.c.Dispatch, func() bool { ps.c.refreshQuota(); return true }); !ok {
		return false
	}
	return true
}

// onPush answers a phone's "push" request; false ends the connection.
func (ps *phoneSession) onPush(r remote.Request) bool {
	var registration notification.Registration
	var message string
	if json.Unmarshal(r.Push, &registration) != nil {
		message = "Invalid notification registration"
	} else {
		result, _ := async.OnMain(ps.ctx, ps.c.Dispatch, func() string {
			if ps.c.Push == nil {
				return "Notification service unavailable"
			}
			if err := ps.c.Push.Register(registration); err != nil {
				return err.Error()
			}
			return ""
		})
		message = result
		if message == "" {
			ps.deviceID = registration.ID
		}
		if message == "" && !registration.Disabled {
			if _, err := apns.OpenProvider(appdir.Dir()); err != nil {
				message = "Phone registered. APNs is not configured on the desktop; run mygo push setup."
			}
		}
	}
	if !ps.send(remote.Frame{Type: "push", Error: message}) {
		return false
	}
	return true
}

// onRead answers a phone's "read", "read-all" request; false ends the connection.
func (ps *phoneSession) onRead(r remote.Request) bool {
	n, _ := async.OnMain(ps.ctx, ps.c.Dispatch, func() notification.PhoneState {
		if r.Op == "read" {
			ps.c.MarkPhoneCompletionRead(r)
		}
		if ps.c.Push == nil || ps.deviceID == "" {
			return notification.PhoneState{}
		}
		if r.Op == "read-all" {
			return ps.c.Push.MarkAllRead(ps.deviceID, r.ReadVersion)
		}
		target := ps.c.PhoneThreadFor(r)
		if !target.Found {
			return ps.c.Push.State(ps.deviceID)
		}
		notice := notification.Notice{Thread: fmt.Sprint(target.Thread.ID), ServerID: target.Thread.ServerID, Agent: model.ThreadAgent(target.Thread), Project: target.cwd}
		return ps.c.Push.MarkRead(ps.deviceID, notice.UnreadKey(), r.ReadVersion)
	})
	data, _ := json.Marshal(n)
	if !ps.send(remote.Frame{Type: "read", Data: data}) {
		return false
	}
	return true
}

// onSubscribe answers a phone's "subscribe" request; false ends the connection.
func (ps *phoneSession) onSubscribe(r remote.Request) bool {
	// Retry the same thread too: an earlier history read may have failed
	// while the desktop was still reconnecting.
	ps.subscription = r
	ps.sub, ps.lastRevision, ps.read, ps.fetched = r.Thread, -1, nil, false
	ps.sync = phoneserver.NewThreadSync(r.Have)
	return true
}

// onSend answers a phone's "send" request; false ends the connection.
func (ps *phoneSession) onSend(r remote.Request) bool {
	if ps.pending != nil {
		if !ps.send(remote.Frame{Type: "sent", Error: "The previous message is still being sent"}) {
			return false
		}
	} else {
		paths, err := phoneserver.SaveImages(r.Images)
		r.Images = nil
		if err != nil {
			if !ps.send(remote.Frame{Type: "sent", Error: err.Error()}) {
				return false
			}
		} else {
			ps.pending = &PhoneSendReq{Req: r, paths: paths, until: time.Now().Add(30 * time.Second)}
		}
	}
	return true
}

// onPrefetch answers a phone's "prefetch" request; false ends the connection.
func (ps *phoneSession) onPrefetch(r remote.Request) bool {
	async.OnMain(ps.ctx, ps.c.Dispatch, func() bool {
		if ref, ok := model.ParseThreadURL(r.Reference); ok {
			ps.c.PrefetchReference(ref)
		}
		return true
	})
	return true
}

// onStop answers a phone's "stop" request; false ends the connection.
func (ps *phoneSession) onStop(r remote.Request) bool {
	async.OnMain(ps.ctx, ps.c.Dispatch, func() bool {
		if t := ps.c.PhoneThreadFor(r); t.Found {
			ps.c.StopThread(t.Thread.ID)
		}
		return true
	})
	return true
}

// onTask answers a phone's "task" request; false ends the connection.
func (ps *phoneSession) onTask(r remote.Request) bool {
	message, _ := async.OnMain(ps.ctx, ps.c.Dispatch, func() string {
		t := ps.c.PhoneThreadFor(r)
		if !t.Found {
			return "Conversation is no longer on the desktop"
		}
		id, action, _ := strings.Cut(r.Text, " ")
		return ps.c.TaskAction(t.Thread.ID, id, action)
	})
	if !ps.send(remote.Frame{Type: "action", Thread: r.Thread, Error: message}) {
		return false
	}
	return true
}

// onRetry answers a phone's "retry" request; false ends the connection.
func (ps *phoneSession) onRetry(r remote.Request) bool {
	message, _ := async.OnMain(ps.ctx, ps.c.Dispatch, func() string {
		t := ps.c.PhoneThreadFor(r)
		if !t.Found {
			return "Conversation is no longer on the desktop"
		}
		if err := ps.c.RetryResponse(appmodel.RetryRequest{ThreadID: t.Thread.ID, MessageID: r.MessageID}); err != nil {
			return err.Error()
		}
		return ""
	})
	if !ps.send(remote.Frame{Type: "action", Thread: r.Thread, Error: message}) {
		return false
	}
	return true
}

// onQueueAction answers a phone's "dequeue", "steer-queued", "drain-queue" request; false ends the connection.
func (ps *phoneSession) onQueueAction(r remote.Request) bool {
	message, _ := async.OnMain(ps.ctx, ps.c.Dispatch, func() string {
		if err := ps.c.PhoneQueueAction(r); err != nil {
			return err.Error()
		}
		return ""
	})
	if !ps.send(remote.Frame{Type: "action", Thread: r.Thread, Error: message}) {
		return false
	}
	return true
}

// onRespond answers a phone's "respond" request; false ends the connection.
func (ps *phoneSession) onRespond(r remote.Request) bool {
	message, _ := async.OnMain(ps.ctx, ps.c.Dispatch, func() string {
		if err := ps.c.PhoneApprovalResponse(r); err != nil {
			return err.Error()
		}
		return ""
	})
	data, _ := json.Marshal(phoneproto.ApprovalReply{ID: r.Approval})
	if !ps.send(remote.Frame{Type: "responded", Data: data, Error: message}) {
		return false
	}
	return true
}

// onCommands answers a phone's "commands" request; false ends the connection.
func (ps *phoneSession) onCommands(r remote.Request) bool {
	ps.cmdWant = r.Project
	ps.cmdAgent = r.Agent
	if ps.cmdAgent == "" {
		ps.cmdAgent = agentmeta.LegacyCommandAgent()
	}
	return true
}
