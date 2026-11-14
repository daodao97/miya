package client

import (
	"strings"

	"miya/internal/chat/model"
)

func (m *Client) sendDraft(steer bool) {
	if m.Sending {
		return
	}
	if !m.CanSend() {
		m.SendErr = m.disconnectedSendError()
		return
	}
	if model.HasAgentReferences(m.Draft, nil) && (m.State.Collaboration == nil || m.State.Collaboration.Version < 1) {
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
	r.Text = strings.TrimSpace(m.Draft)
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
	if r.Text == "" && len(m.Images) == 0 {
		return
	}
	m.BeginSend(r, m.Images)
	m.transmitSend(m.sending)
}

func (m *Client) stop() {
	if conn, id := m.Conn, m.Open; m.CanSend() && id != 0 {
		r := m.ThreadRequest("stop", id)
		go m.write(conn, r)
	}
}
