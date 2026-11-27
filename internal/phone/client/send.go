package client

import (
	"os"
	"strings"
	"time"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	phonetransfer "miya/internal/phone/transfer"
	"miya/internal/remote"
)

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
	waitingForConnection bool
	failure, stage       string
}

type mobileSendProjection struct {
	open, revision int
	epoch          uint64
	messages       []model.Message
}

func (m *Client) BeginSend(r remote.Request, images []string) {
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

}

func sendStage(images []string) string {
	if len(images) > 0 {
		return tr("Uploading images…")
	}
	return tr("Sending…")
}

func (p *mobilePendingSend) remember(t model.Thread) {
	for _, msg := range t.Messages {
		p.beforeMessages[msg.ID] = true
	}
	for _, q := range t.Queue {
		p.beforeQueue[q.ID] = true
	}
}

func (m *Client) composingNewThread(p *mobilePendingSend) bool {
	return m.Open == 0 && m.NewProject == p.request.Project && m.composerEpoch == p.epoch
}

func (m *Client) unsupportedAgentTags(text string) bool {
	return (m.State.Collaboration == nil || m.State.Collaboration.Version < 1) && model.HasAgentReferences(text, nil)
}

func (m *Client) resumePendingSend() {
	p := m.sending
	if p == nil || !p.waitingForConnection || p.desktop != m.Link || !m.CanSend() {
		return
	}
	if m.unsupportedAgentTags(p.request.Text) {
		m.SendErr = tr("Update the Miya background service to use Agent tags")
		m.failPendingSend()
		return
	}
	p.waitingForConnection = false
	p.stage = sendStage(p.Message.Images)
	m.transmitSend(p)
}

func (m *Client) transmitSend(p *mobilePendingSend) {
	conn, gen := m.Conn, m.Generation
	p.attempt++
	attempt := p.attempt
	paths := append([]string(nil), p.Message.Images...)
	r := p.request
	go func() {
		images, err := phonetransfer.ReadImages(paths)
		if err == nil {
			r.Images = images
			err = m.write(conn, r)
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
		})
	}()
}

func (m *Client) failPendingSend() {
	if p := m.sending; p != nil {
		p.failure, p.waitingForConnection = m.SendErr, false
		m.sending = nil
		m.SendErr = ""
	}
	m.Sending = false
	m.prunePendingSends()
}

func (m *Client) retrySend(p *mobilePendingSend) {
	if m.Sending || p.desktop != m.Link {
		return
	}
	if !m.CanSend() {
		p.failure = m.disconnectedSendError()
		return
	}
	p.failure, p.stage = "", sendStage(p.Message.Images)
	m.sending, m.Sending, m.SendErr = p, true, ""
	m.transmitSend(p)
}

func (m *Client) editPendingSend(p *mobilePendingSend) {
	if m.Sending || m.Draft != "" || len(m.Images) != 0 {
		return
	}
	m.Draft, m.Images = p.Message.Content, p.Message.Images
	p.Message.Images = nil // the composer owns the files again
	p.echoed = true
	m.prunePendingSends()
}

func (m *Client) pendingFor(p *mobilePendingSend, cur model.Thread) bool {
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

func (m *Client) PendingSendThread(cur model.Thread) (model.Thread, bool) {
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
	return strings.TrimSpace(msgfmt.ThreadContextDisplay(a)) == strings.TrimSpace(msgfmt.ThreadContextDisplay(b))
}

func (p *mobilePendingSend) matches(msg model.Message) bool {
	return msg.Role == "user" && samePendingText(msg.Content, p.Message.Content) && max(len(msg.Images), len(msg.Attachments)) >= len(p.Message.Images)
}

func (m *Client) receivePendingSends(t model.Thread) {
	if len(m.Outbox) == 0 {
		return
	}
	m.pendingProjection = nil
	usedMessages, usedQueue := map[int]bool{}, map[int]bool{}
	for _, p := range m.Outbox {
		if p.echoed || !m.pendingFor(p, t) {
			continue
		}
		if !p.waitingForConnection && p.request.ServerID == "" && t.ServerID != "" {
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
			if !p.waitingForConnection && seen > p.beforeUsers && !p.beforeMessages[msg.ID] && !usedMessages[msg.ID] {
				p.echoed, usedMessages[msg.ID] = true, true
				p.retainedImages = m.keepSentImagePreviews(p.Message.Images, msg.Images)
				break
			}
		}
		if !p.waitingForConnection && !p.echoed {
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

func (m *Client) prunePendingSends() {
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

	}
}

func (m *Client) Sent(id int, msg string) {
	p := m.sending
	if !m.Sending || p == nil || p.waitingForConnection {
		return
	}
	if msg != "" {
		m.SendErr = msg
		m.failPendingSend()
		return
	}
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
}

type PendingStatus struct {
	ID             int
	Stage, Failure string
	Acknowledged   bool
}

func (m *Client) Pending() []PendingStatus {
	var out []PendingStatus
	for _, p := range m.Outbox {
		if !p.echoed && m.pendingFor(p, m.Viewer.Current()) {
			out = append(out, PendingStatus{p.Message.ID, p.stage, p.failure, p.acknowledged})
		}
	}
	return out
}
func (m *Client) RetryPending(id int) {
	for _, p := range m.Outbox {
		if p.Message.ID == id {
			m.retrySend(p)
			return
		}
	}
}
func (m *Client) EditPending(id int) {
	for _, p := range m.Outbox {
		if p.Message.ID == id {
			m.editPendingSend(p)
			return
		}
	}
}
