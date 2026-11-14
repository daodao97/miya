package client

import (
	"encoding/json"
	"strings"
	"time"

	"miya/internal/automation"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
)

type AutomationForm struct {
	Request, Project, Agent string
	Draft                   *automation.View
	Enabled                 bool
	Pending, Op             string
	RequestedAt             time.Time
	Error                   string
	// Keep both the key and payload after an uncertain send, so retrying
	// creates exactly the same automation, including across reconnects.
	CreateKey     string
	CreatePayload json.RawMessage
}

func (m *Client) OpenAutomationCreate() {
	f := &m.Autos.Form
	if f.Project == "" {
		*f = AutomationForm{Project: m.SelectedProject().Path, Agent: m.composerAgent(), Enabled: true}
	}

}

func (m *Client) SendAutomationForm(op string) {
	f := &m.Autos.Form
	if f.Pending != "" || !m.Connected || !m.CanSend() || !m.State.AutomationCreate {
		return
	}
	r := remote.Request{Op: op, RequestID: automation.NewID() + automation.NewID(), Text: f.Request, Project: f.Project, Agent: f.Agent}
	switch op {
	case "automation-draft":
		if strings.TrimSpace(f.Request) == "" || f.CreateKey != "" {
			return
		}
		f.Draft = nil
	case "automation-create":
		if f.Draft == nil {
			return
		}
		if f.CreateKey == "" {
			a := automation.Clone(f.Draft.Automation)
			a.ID, a.Project, a.Agent, a.Request, a.Enabled = "", f.Project, f.Agent, strings.TrimSpace(f.Request), f.Enabled
			if err := a.Normalize(); err != nil {
				f.Error = err.Error()
				return
			}
			f.CreateKey = r.RequestID
			f.CreatePayload, _ = json.Marshal(a)
		}
		r.RequestID, r.Automation = f.CreateKey, f.CreatePayload
	default:
		return
	}
	f.Pending, f.Op, f.Error, f.RequestedAt = r.RequestID, op, "", time.Now()
	conn, gen := m.Conn, m.Generation
	requestedAt := f.RequestedAt
	go func() {
		if err := m.write(conn, r); err != nil {
			m.onMain(gen, func() {
				if m.Conn == conn && f.Pending == r.RequestID && f.RequestedAt == requestedAt {
					f.Pending, f.Error = "", tr("Failed to send. Try again: ")+err.Error()
				}
			})
		}
	}()
}

func (m *Client) ReceiveAutomationForm(op string, raw json.RawMessage, message string) {
	f := &m.Autos.Form
	var result phoneproto.AutomationFormResult
	if json.Unmarshal(raw, &result) != nil || result.RequestID == "" || result.RequestID != f.Pending || op != f.Op {
		return
	}
	f.Pending, f.Error = "", message
	if message != "" {
		if op == "automation-create" {
			f.CreateKey, f.CreatePayload = "", nil
		}
		return
	}
	if result.View == nil || result.View.ID == "" {
		f.Error = tr("Failed to load automation data. Try again.")
		return
	}
	if op == "automation-draft" {
		f.Draft = result.View
		return
	}
	v := *result.View
	replaced := false
	for i := range m.Autos.List {
		if m.Autos.List[i].ID == v.ID {
			m.Autos.List[i], replaced = v, true
		}
	}
	if !replaced {
		m.Autos.List = append(m.Autos.List, v)
	}
	m.Autos.Loaded, m.Autos.Err, m.Autos.RequestedAt = true, "", time.Time{}
	*f = AutomationForm{}
	m.Autos.Detail = v.ID

}

func (m *Client) InterruptAutomationForm() {
	f := &m.Autos.Form
	if f.Pending != "" {
		f.Pending, f.Error = "", tr("Disconnected. Reconnect to try again.")
	}
}
