package client

import (
	"strings"

	"miya/internal/chat/model"
)

func (m *Client) sendDraft(steer bool) {
	if m.Sending {
		return
	}
	text := strings.TrimSpace(m.Draft)
	if text == "" && len(m.Images) == 0 {
		return
	}
	offline := !m.CanSend()
	if offline && !m.retryNow() {
		m.SendErr = tr("Not connected to desktop")
		return
	}
	if !offline && m.unsupportedAgentTags(text) {
		m.SendErr = tr("Update the Miya background service to use Agent tags")
		return
	}
	if m.Open == 0 && m.NewProject == "" {
		project := m.selectedProject()
		if project.Path == "" {
			m.SendErr = tr("Add a project on your desktop first")
			return
		}
		m.NewProject, m.NewAgent = project.Path, m.composerAgent()
		m.Path = "/thread"
	}
	r := m.ThreadRequest("send", m.Open)
	if offline && r.Thread != 0 && r.ServerID == "" {
		m.SendErr = tr("Available once the session is created")
		return
	}
	r.Text = text
	r.Steer = steer
	agent := m.composerAgent()
	if m.Open != 0 {
		agent = model.ThreadAgent(m.Viewer.Current())
	}
	if len(m.State.Agents) > 0 {
		o := m.SendOptions(agent)
		r.Options = &o
	}
	if m.Open == 0 {
		r.Project, r.Agent = m.NewProject, m.NewAgent
	}
	m.BeginSend(r, m.Images)
	if offline {
		m.sending.waitingForConnection = true
		m.sending.stage = tr("Will send when the desktop reconnects…")
		return
	}
	m.transmitSend(m.sending)
}

func (m *Client) stop() {
	if conn, id := m.Conn, m.Open; m.CanSend() && id != 0 {
		r := m.ThreadRequest("stop", id)
		go m.write(conn, r)
	}
}
