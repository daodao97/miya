package client

import (
	"fmt"
	"path/filepath"
	"time"

	"miya/internal/agent"
	"miya/internal/chat/model"
	"miya/internal/notification"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	svcproto "miya/internal/service/protocol"
)

func (m *Client) ReceiveThread(id int, d phoneproto.ThreadDelta) {
	if m.Open != id {
		return
	}
	cur := m.Viewer.Current()
	if cur.ID == id && cur.ServerID != "" {
		if d.Thread.ServerID == "" || (d.Thread.ServerID == cur.ServerID && (d.Thread.Loading || !d.Thread.HistoryLoaded) && len(d.Order) == 0) {
			// A loading or missing-thread response is not an empty history.
			// Keep the displayed messages and cache until history arrives,
			// including responses from desktops that still send an empty order.
			cur.Status, cur.Loading = d.Thread.Status, d.Thread.Loading
			m.updateThreadView(cur, false)
			return
		}
		if d.Thread.ServerID != cur.ServerID || model.ThreadAgent(d.Thread) != model.ThreadAgent(cur) {
			return
		}
	}
	d.Thread.ID = id
	m.ApplyThread(d)
	m.receivePendingSends(m.Viewer.Current())
	// Only a live delta confirms that the phone has the completed contents;
	// sidebar metadata combined with cached messages does not.
	if model.SameMessageOrder(m.Viewer.Current().Messages, d.Order) {
		m.CompletionReceived = m.completionVersion(d.Thread)
		m.markCompletionRead()
	}
}

func (m *Client) ShowThread(t model.Thread) {
	m.updateThreadView(t, true)
}

func (m *Client) updateThreadView(t model.Thread, messagesChanged bool) {
	v := m.Viewer
	v.SetThreads([]model.Thread{t})
	v.SetActive(t.ID)
	if messagesChanged {
		v.setRevision(v.Revision() + 1)
	}
	if t.Title != "" {
		m.Title = t.Title
	}
}

func (m *Client) Subscribe(id int) {
	m.CompletionRead = notification.DesktopRead{}
	m.CompletionReceived = notification.DesktopRead{}
	if conn := m.Conn; m.CanSend() {
		r := m.SubscribeRequest(id)
		go m.write(conn, r)
	}
}

func (m *Client) find(id int) (model.Thread, model.ProjectSnapshot) {
	for _, p := range m.allPhoneProjects() {
		for _, t := range p.Threads {
			if t.ID == id {
				return t, p
			}
		}
	}
	return model.Thread{}, model.ProjectSnapshot{}
}

func (m *Client) project(path string) model.ProjectSnapshot {
	for _, p := range m.allPhoneProjects() {
		if p.Path == path {
			return p
		}
	}
	return model.ProjectSnapshot{Path: path, Name: filepath.Base(path)}
}

func (m *Client) currentProject() model.ProjectSnapshot {
	for _, p := range m.State.Projects {
		if p.Current {
			return p
		}
	}
	if len(m.State.Projects) > 0 {
		return m.State.Projects[0]
	}
	return model.ProjectSnapshot{}
}

func (m *Client) ConnectionLine() string {
	switch {
	case m.Error != "":
		return m.Error
	case !m.RetryAt.IsZero():
		remaining := time.Until(m.RetryAt)
		if remaining <= 0 {
			return tr("Reconnecting to desktop…")
		}
		seconds := int((remaining + time.Second - 1) / time.Second)
		line := fmt.Sprintf(tr("Disconnected. Retrying in %d seconds."), seconds)
		if m.RetryError != "" {
			line += ": " + m.RetryError
		}
		return line
	case m.Status != "":
		return m.Status
	case !m.Connected:
		return tr("Disconnected")
	}
	return ""
}

func (m *Client) selectedProject() model.ProjectSnapshot {
	if m.Selected != "" {
		for _, p := range m.State.Projects {
			if p.Path == m.Selected {
				return p
			}
		}
	}
	return m.currentProject()
}

func (m *Client) ComposerProject() string {
	if m.Open != 0 {
		if _, p := m.find(m.Open); p.Path != "" {
			return p.Path
		}
	}
	if m.NewProject != "" {
		return m.NewProject
	}
	return m.selectedProject().Path
}

func (m *Client) requestCommands(id, project string) {
	conn := m.Conn
	key := agent.CommandKey(id, project)
	if project == "" || conn == nil || m.Asked[key] {
		return
	}
	if m.Asked == nil {
		m.Asked = map[string]bool{}
	}
	m.Asked[key] = true
	go conn.Send(remote.Request{Op: "commands", Agent: id, Project: project})
}

func (m *Client) composerCommands(id, project string) ([]model.ComposerChoice, bool) {
	if m.agentDescriptor(id).NativeCommands {
		return m.commandChoices(id, project)
	}
	for _, option := range m.State.Agents {
		if option.Agent == id && option.Commands != nil {
			return option.Commands, true
		}
	}
	fallback := agent.DefaultCommands(id)
	return fallback.PhoneChoices, fallback.Known
}

const phoneCachedThreads = 30

func (m *Client) CachedMessages(serverID string) []model.Message {
	if serverID == "" {
		return nil
	}
	if msgs, ok := m.MsgCache[serverID]; ok {
		return msgs
	}
	msgs := m.loadDisk(serverID)
	if len(msgs) > 0 {
		m.keep(serverID, msgs)
	}
	return msgs
}

func (m *Client) Remember(serverID string, msgs []model.Message) {
	m.keep(serverID, msgs)
	m.persist(serverID, msgs)
}

func (m *Client) keep(serverID string, msgs []model.Message) {
	if serverID == "" {
		return
	}
	if m.MsgCache == nil {
		m.MsgCache = map[string][]model.Message{}
	}
	for i, k := range m.msgKeys {
		if k == serverID {
			m.msgKeys = append(m.msgKeys[:i:i], m.msgKeys[i+1:]...)
			break
		}
	}
	m.msgKeys = append(m.msgKeys, serverID)
	m.MsgCache[serverID] = msgs
	for len(m.msgKeys) > phoneCachedThreads {
		delete(m.MsgCache, m.msgKeys[0])
		m.msgKeys = m.msgKeys[1:]
	}
}

func (m *Client) SubscribeRequest(id int) remote.Request {
	r := m.ThreadRequest("subscribe", id)
	if id == 0 {
		return r
	}
	for _, msg := range m.CachedMessages(r.ServerID) {
		r.Have = append(r.Have, remote.Seen{ID: msg.ID, FP: svcproto.MessageFingerprint(msg)})
	}
	return r
}

func (m *Client) ThreadRequest(op string, id int) remote.Request {
	r := remote.Request{Op: op, Thread: id}
	if id == 0 {
		return r
	}
	t, p := m.find(id)
	// The currently displayed page is authoritative when a new state has
	// already reused its numeric ID for a different conversation.
	if cur := m.Viewer.Current(); cur.ID == id && cur.ServerID != "" {
		if cur.ServerID != t.ServerID || model.ThreadAgent(cur) != model.ThreadAgent(t) {
			p = model.ProjectSnapshot{}
		}
		t = cur
	}
	if _, project, ok := m.referencedThread(model.ThreadRef{Source: m.State.ReferenceSource, Backend: model.ThreadAgent(t), SessionID: t.ServerID}); ok {
		p = project
	}
	r.ServerID, r.Agent, r.Project = t.ServerID, model.ThreadAgent(t), p.Path
	return r
}

func (m *Client) ApplyThread(d phoneproto.ThreadDelta) {
	t := d.Thread
	var base []model.Message
	if !d.Reset {
		boundIdentity := false
		if cur := m.Viewer.Current(); cur.ID == t.ID && (cur.ServerID == t.ServerID || cur.ServerID == "" && model.ThreadAgent(cur) == model.ThreadAgent(t)) {
			// A new thread's first messages can arrive before its durable ID.
			// Binding that ID changes metadata, not the history already received
			// under its numeric ID; the desktop need not resend those messages.
			base = cur.Messages
			boundIdentity = cur.ServerID == "" && t.ServerID != ""
		}
		if len(base) == 0 {
			base = m.CachedMessages(t.ServerID)
		}
		// Busy, loading, usage and title updates carry no new messages.
		// Reuse the history and its presentation groups instead of copying,
		// regrouping and persisting the entire conversation on each update.
		if len(d.Changed) == 0 && model.SameMessageOrder(base, d.Order) {
			t.Messages = base
			if boundIdentity {
				m.Remember(t.ServerID, base)
			}
			m.updateThreadView(t, false)
			return
		}
	}
	known := make(map[int]model.Message, len(base)+len(d.Changed))
	for _, x := range base {
		known[x.ID] = x
	}
	for _, x := range d.Changed {
		known[x.ID] = x
	}
	list := make([]model.Message, 0, len(d.Order))
	for _, id := range d.Order {
		if x, ok := known[id]; ok {
			list = append(list, x)
		}
	}
	t.Messages = list
	m.Remember(t.ServerID, list)
	m.ShowThread(t)
}

func (m *Client) allPhoneProjects() []model.ProjectSnapshot { return m.State.Projects }
