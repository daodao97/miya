package client

import (
	"encoding/json"
	"fmt"
	"time"

	"miya/internal/automation"
	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
)

type Automations struct {
	Webhook        *automation.WebhookView
	Briefing       *automation.BriefingView
	Form           AutomationForm
	List           []automation.View
	Detail         string
	Loaded         bool
	Loading        bool
	RequestedAt    time.Time
	Err            string
	RunRequest     string
	RunID          string
	RunRequestedAt time.Time
	RunError       string
}

func (m *Client) RunAutomation(id string) {
	a := &m.Autos
	if a.RunRequest != "" || !m.Connected || !m.CanSend() || !m.State.AutomationRun {
		return
	}
	for _, v := range a.List {
		if v.ID == id && v.Running {
			return
		}
	}
	a.RunRequestedAt = time.Now()
	a.RunRequest, a.RunID, a.RunError = fmt.Sprint(a.RunRequestedAt.UnixNano()), id, ""
	request := remote.Request{Op: "automation-run", AutomationID: id, RequestID: a.RunRequest}
	conn, gen := m.Conn, m.Generation
	go func() {
		if err := m.write(conn, request); err != nil {
			m.onMain(gen, func() {
				if m.Conn == conn && a.RunRequest == request.RequestID {
					a.RunRequest, a.RunError = "", tr("Failed to send. Refresh to check run status: ")+err.Error()
				}
			})
		}
	}()
}

func (m *Client) ReceiveAutomationRun(data json.RawMessage, message string) {
	var result phoneproto.AutomationRunResult
	if json.Unmarshal(data, &result) != nil || result.RequestID == "" || result.RequestID != m.Autos.RunRequest {
		return
	}
	a := &m.Autos
	a.RunRequest, a.RunError = "", message
	if result.List != nil {
		a.List, a.Loaded, a.Err = result.List, true, ""
	}
	a.RequestedAt = time.Time{}
}

func (m *Client) interruptFeatureRequests() {
	m.InterruptCollaborationRequests()
	m.InterruptAutomationForm()
	m.Autos.Loading = false
	m.Memories.Loading = false
	m.Autos.RequestedAt = time.Time{}
	m.Memories.RequestedAt = time.Time{}
	if m.Autos.RunRequest != "" {
		m.Autos.RunRequest, m.Autos.RunError = "", tr("Disconnected. Refresh to check run status.")
	}
}

func (m *Client) LoadAutomations() {
	if m.Autos.Loading || !m.Connected || !m.CanSend() || !m.State.Automations {
		return
	}
	m.Autos.Loading, m.Autos.RequestedAt = true, time.Now()
	conn, gen := m.Conn, m.Generation
	webhookSupported, briefingSupported := m.State.AutomationWebhook, m.State.AutomationBriefing
	go func() {
		if webhookSupported {
			_ = m.write(conn, remote.Request{Op: "automation-webhook"})
		}
		if briefingSupported {
			_ = m.write(conn, remote.Request{Op: "automation-briefing"})
		}
		if err := m.write(conn, remote.Request{Op: "automations"}); err != nil {
			m.onMain(gen, func() {
				if m.Conn == conn {
					m.Autos.Loading, m.Autos.Err = false, tr("Failed to load: ")+err.Error()
				}
			})
		}
	}()
}

func (m *Client) ReceiveAutomations(data json.RawMessage, message string) {
	m.Autos.Loading = false
	if message != "" {
		m.Autos.Err = message
		return
	}
	var list []automation.View
	if err := json.Unmarshal(data, &list); err != nil {
		m.Autos.Err = tr("Failed to load automation data. Try again.")
		return
	}
	m.Autos.List, m.Autos.Loaded, m.Autos.Err = list, true, ""
}

func (m *Client) AutomationRunThread(v automation.View, r automation.Run) (model.Thread, bool) {
	return phoneproto.AutomationRunThread(m.State.Projects, m.defaultAgent(), v, r)
}

func (m *Client) SetAutomationBriefing(at string) {
	if m.State.AutomationBriefing {
		m.automationRequest("automation-briefing", automation.Request{Action: "set", Data: at})
	}
}

func (m *Client) ReviewAutomation(req automation.Request) {
	if m.State.AutomationReview {
		m.automationRequest("automation-review", req)
	}
}

func (m *Client) automationRequest(op string, req automation.Request) {
	if !m.Connected || !m.CanSend() {
		return
	}
	body, err := json.Marshal(req)
	if err != nil {
		m.Autos.Err = err.Error()
		return
	}
	conn, gen := m.Conn, m.Generation
	go func() {
		if err := m.write(conn, remote.Request{Op: op, Automation: body}); err != nil {
			m.onMain(gen, func() {
				if m.Conn == conn {
					m.Autos.Err = err.Error()
				}
			})
		}
	}()
}
