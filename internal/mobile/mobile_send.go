package mobile

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	conversation "miya/internal/chat/view"
	phonetransfer "miya/internal/phone/transfer"
	"miya/internal/remote"
	"miya/internal/uiruntime"
	"miya/internal/widgets"
)

// Pending messages belong to the phone until live history (or the desktop's
// queue) echoes them. They never enter synchronized history, Have or disk caches.
type mobilePendingSend struct {
	Message              model.Message
	request              remote.Request
	desktop              string
	thread               int
	attempt              uint64
	epoch                uint64
	beforeMessages       map[int]bool
	beforeQueue          map[int]bool
	beforeUsers          int
	retainedImages       map[string]bool
	acknowledged, echoed bool
	failure, stage       string
}

type mobileSendProjection struct {
	open, revision int
	epoch          uint64
	messages       []model.Message
}

// BeginSend moves the submitted draft and photos into the conversation at once.
// Upload and acknowledgement must not own or later clear the next draft.
func (m *App) BeginSend(r remote.Request, images []string) {
	if r.Agent == "" {
		r.Agent = m.composerAgent()
	}
	m.nextSendID--
	p := &mobilePendingSend{
		Message: model.Message{ID: m.nextSendID, Role: "user", Content: r.Text,
			Images: append([]string(nil), images...), SentAtMs: time.Now().UnixMilli()},
		request: r, desktop: m.Link, thread: m.Open, epoch: m.composerEpoch,
		beforeMessages: map[int]bool{}, beforeQueue: map[int]bool{},
	}
	cur := m.Viewer.Current()
	p.remember(cur)
	for _, msg := range cur.Messages {
		if p.matches(msg) {
			p.beforeUsers++
		}
	}
	m.Outbox = append(m.Outbox, p)
	m.pendingProjection = nil
	m.sending, m.Sending, m.SendErr = p, true, ""
	p.stage = sendStage(images)
	m.Draft, m.Images = "", nil
	m.View.Groups = conversation.GroupCache{}
	id := m.Open
	if id == 0 {
		id = -1
	}
	m.View.ListFor(id).ScrollToEnd()
}

func sendStage(images []string) string {
	if len(images) > 0 {
		return tr("Uploading images…")
	}
	return tr("Sending…")
}

// remember marks t's current messages and queue as not being this send.
func (p *mobilePendingSend) remember(t model.Thread) {
	for _, msg := range t.Messages {
		p.beforeMessages[msg.ID] = true
	}
	for _, q := range t.Queue {
		p.beforeQueue[q.ID] = true
	}
}

// composingNewThread reports whether the composer still shows p's new conversation.
func (m *App) composingNewThread(p *mobilePendingSend) bool {
	return m.Open == 0 && m.NewProject == p.request.Project && m.composerEpoch == p.epoch
}

func (m *App) transmitSend(p *mobilePendingSend) {
	conn, gen := m.Conn, m.Generation
	p.attempt++
	attempt := p.attempt
	paths := append([]string(nil), p.Message.Images...)
	r := p.request
	go func() {
		images, err := phonetransfer.ReadImages(paths)
		if err == nil {
			r.Images = images
			err = conn.Send(r)
		}
		m.onMain(gen, func() {
			if m.sending != p || p.attempt != attempt {
				return
			}
			if err != nil {
				m.SendErr = tr("Failed to send: ") + err.Error()
				m.failPendingSend()
			} else {
				p.stage = tr("Waiting for desktop…")
			}
			uiruntime.Invalidate()
		})
	}()
}

func (m *App) failPendingSend() {
	if p := m.sending; p != nil {
		p.failure = m.SendErr
		m.sending = nil
		m.SendErr = ""
	}
	m.Sending = false
	m.prunePendingSends()
}

func (m *App) retrySend(p *mobilePendingSend) {
	if m.Sending || p.desktop != m.Link {
		return
	}
	if m.Conn == nil {
		p.failure = m.disconnectedSendError()
		return
	}
	p.failure, p.stage = "", sendStage(p.Message.Images)
	m.sending, m.Sending, m.SendErr = p, true, ""
	m.transmitSend(p)
}

func (m *App) editPendingSend(p *mobilePendingSend) {
	if m.Sending || m.Draft != "" || len(m.Images) != 0 {
		return
	}
	m.Draft, m.Images = p.Message.Content, p.Message.Images
	p.Message.Images = nil // the composer owns the files again
	p.echoed = true
	m.prunePendingSends()
}

func (m *App) pendingFor(p *mobilePendingSend, cur model.Thread) bool {
	if p.desktop != m.Link {
		return false
	}
	if p.thread == 0 {
		return m.composingNewThread(p)
	}
	if p.request.ServerID != "" && cur.ServerID != "" {
		return p.request.ServerID == cur.ServerID && (p.request.Agent == "" || p.request.Agent == model.ThreadAgent(cur))
	}
	return p.thread == m.Open && cur.ID == m.Open
}

// PendingSendThread overlays local messages without mutating authoritative data.
func (m *App) PendingSendThread(cur model.Thread) (model.Thread, bool) {
	shown := false
	cache := m.pendingProjection
	valid := cache != nil && cache.open == m.Open && cache.epoch == m.composerEpoch && cache.revision == m.Viewer.Revision()
	for _, p := range m.Outbox {
		if p.echoed || !m.pendingFor(p, cur) {
			continue
		}
		if !shown {
			if m.Open == 0 {
				cur = model.Thread{ID: -1, Agent: m.NewAgent}
			}
			cur.Loading = false
			shown = true
			if valid {
				cur.Messages = cache.messages
				return cur, true
			}
			cur.Messages = append([]model.Message(nil), cur.Messages...)
		}
		cur.Messages = append(cur.Messages, p.Message)
	}
	if shown {
		m.pendingProjection = &mobileSendProjection{open: m.Open, epoch: m.composerEpoch, revision: m.Viewer.Revision(), messages: cur.Messages}
	}
	return cur, shown
}

func samePendingText(a, b string) bool {
	return strings.TrimSpace(appmodel.ThreadContextDisplay(a)) == strings.TrimSpace(appmodel.ThreadContextDisplay(b))
}

func (p *mobilePendingSend) matches(msg model.Message) bool {
	return msg.Role == "user" && samePendingText(msg.Content, p.Message.Content) && max(len(msg.Images), len(msg.Attachments)) >= len(p.Message.Images)
}

func (m *App) receivePendingSends(t model.Thread) {
	if len(m.Outbox) == 0 {
		return
	}
	m.pendingProjection = nil
	usedMessages, usedQueue := map[int]bool{}, map[int]bool{}
	for _, p := range m.Outbox {
		if p.echoed || !m.pendingFor(p, t) {
			continue
		}
		if p.request.ServerID == "" && t.ServerID != "" {
			p.request.ServerID = t.ServerID
		}
		seen := 0
		for _, msg := range t.Messages {
			if !p.matches(msg) {
				continue
			}
			seen++
			// Resuming a thread can replace every runtime ID in its old
			// history. Those earlier occurrences are still not this send.
			if seen > p.beforeUsers && !p.beforeMessages[msg.ID] && !usedMessages[msg.ID] {
				p.echoed, usedMessages[msg.ID] = true, true
				p.retainedImages = m.keepSentImagePreviews(p.Message.Images, msg.Images)
				break
			}
		}
		if !p.echoed {
			for _, q := range t.Queue {
				if !p.beforeQueue[q.ID] && !usedQueue[q.ID] && samePendingText(q.Text, p.Message.Content) && len(q.Attachments) >= len(p.Message.Images) {
					p.echoed, usedQueue[q.ID] = true, true
					break
				}
			}
		}
		// An older echo must not satisfy a later identical send on the next
		// metadata-only delta, after the earlier preview has been removed.
		p.remember(t)
		if !p.echoed {
			p.beforeUsers = max(p.beforeUsers, seen)
		}
	}
	m.prunePendingSends()
}

func (m *App) prunePendingSends() {
	before := len(m.Outbox)
	kept := m.Outbox[:0]
	for _, p := range m.Outbox {
		if p.echoed && m.sending != p {
			for _, path := range p.Message.Images {
				if !p.retainedImages[path] {
					go os.Remove(path)
				}
			}
			continue
		}
		kept = append(kept, p)
	}
	clear(m.Outbox[len(kept):])
	m.Outbox = kept
	if len(kept) != before {
		m.pendingProjection = nil
		m.View.Groups = conversation.GroupCache{}
	}
}

func (m *App) pendingSendStatus(c *ui.Context) {
	var edit *mobilePendingSend
	for _, p := range m.Outbox {
		if p.echoed || !m.pendingFor(p, m.Viewer.Current()) {
			continue
		}
		ui.Column(c.Key(fmt.Sprintf("pending-send-status:%d", p.Message.ID))).FillWidth().Gap(6).Padding(6, 12).Children(func() {
			if p.failure != "" {
				ui.Text(c, p.failure).FontSize(m.Font(12)).TextColor(widgets.Danger).FillWidth().MaxLines(2)
				ui.Row(c).Gap(8).Wrap().Children(func() {
					if m.pill(c, "retry", tr("Retry send"), false).Disabled(m.Sending).Clicked() {
						m.retrySend(p)
					}
					if m.pill(c, "edit", tr("Edit message"), false).Disabled(m.Sending || m.Draft != "" || len(m.Images) != 0).Clicked() {
						edit = p
					}
				})
				return
			}
			label := p.stage
			if p.acknowledged {
				label = tr("Syncing conversation…")
			}
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Spinner(c).FontSize(m.Font(12)).TextColor(m.Pal.muted)
				ui.Text(c, label).FontSize(m.Font(12)).TextColor(m.Pal.muted)
			})
		})
	}
	if edit != nil {
		m.editPendingSend(edit)
	}
}

// Sent applies acknowledgement to the submitted message, never to the current
// composer or a different conversation the user opened during upload.
func (m *App) Sent(id int, msg string) {
	p := m.sending
	if !m.Sending || p == nil {
		return
	}
	if msg != "" {
		m.SendErr = msg
		m.failPendingSend()
		mobileFeedback(mygo.Haptics.Error)
		return
	}
	mobileFeedback(mygo.Haptics.Light)
	m.sending, m.Sending, m.SendErr = nil, false, ""
	p.acknowledged = true
	wasNew := p.thread == 0
	if id != 0 {
		p.thread = id
	}
	if wasNew && id != 0 && m.composingNewThread(p) {
		m.Open, m.NewProject = id, ""
		m.ShowThread(model.Thread{ID: id, Agent: p.request.Agent, Loading: true})
		m.Subscribe(id)
	}
	m.receivePendingSends(m.Viewer.Current())
	if id != 0 && m.Open == id {
		m.View.ListFor(id).ScrollToEnd()
	}
}
