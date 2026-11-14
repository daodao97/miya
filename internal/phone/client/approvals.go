package client

import (
	"encoding/json"
	"time"

	"miya/internal/interaction"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
	"miya/internal/textutil"
)

func (m *Client) RequestPending(a interaction.Request) bool {
	return m.ApprovalPending[string(a.ID)] != ""
}
func (m *Client) RequestError(a interaction.Request) string { return m.ApprovalErrors[string(a.ID)] }
func (m *Client) approvalError(id, message string) {
	if m.ApprovalErrors == nil {
		m.ApprovalErrors = map[string]string{}
	}
	m.ApprovalErrors[id] = message
}

// SubmitApproval sends only an explicit user response to the current request
// version. A state frame never supplies consent.
func (m *Client) SubmitApproval(a interaction.Request, reply interaction.Response, remember bool) {
	id := string(a.ID)
	if m.RequestPending(a) {
		return
	}
	current := false
	for _, pending := range m.State.Approvals {
		if textutil.SameJSON(pending.ID, a.ID) && phoneproto.ApprovalFingerprint(pending) == phoneproto.ApprovalFingerprint(a) {
			current = true
			break
		}
	}
	if !current {
		return
	}
	if !m.Connected || !m.CanSend() {
		m.approvalError(id, tr("Not connected to desktop"))
		return
	}
	raw, err := json.Marshal(phoneproto.LegacyResponse(a, reply))
	if err != nil {
		m.approvalError(id, err.Error())
		return
	}
	if m.ApprovalPending == nil {
		m.ApprovalPending = map[string]string{}
		m.approvalTimers = map[string]*time.Timer{}
	}
	version := phoneproto.ApprovalFingerprint(a)
	m.ApprovalPending[id] = version
	delete(m.ApprovalErrors, id)
	conn, gen := m.Conn, m.Generation
	var timer *time.Timer
	finish := func(message string) {
		m.onMain(gen, func() {
			if m.ApprovalPending[id] == version && m.approvalTimers[id] == timer {
				m.clearApprovalPending(id)
				m.approvalError(id, message)
			}
		})
	}
	timer = time.AfterFunc(30*time.Second, func() { finish(tr("Request timed out. Try again.")) })
	m.approvalTimers[id] = timer
	r := remote.Request{Op: "respond", Approval: a.ID, ApprovalVersion: version, Response: &reply, Result: raw, Allow: reply.Action == interaction.Allow, Remember: remember}
	go func() {
		if err := m.write(conn, r); err != nil {
			finish(tr("Failed to submit: ") + err.Error())
		}
	}()
}
func (m *Client) clearApprovalPending(id string) {
	if timer := m.approvalTimers[id]; timer != nil {
		timer.Stop()
	}
	delete(m.approvalTimers, id)
	delete(m.ApprovalPending, id)
}
func (m *Client) ApprovalAnswered(id json.RawMessage, message string) {
	key := string(id)
	version := m.ApprovalPending[key]
	// Unsolicited/late acknowledgements are reconciled by the next state frame.
	if version == "" {
		return
	}
	m.clearApprovalPending(key)
	if message != "" {
		m.approvalError(key, message)
		return
	}
	out := m.State.Approvals[:0]
	for _, a := range m.State.Approvals {
		if !textutil.SameJSON(a.ID, id) || phoneproto.ApprovalFingerprint(a) != version {
			out = append(out, a)
		}
	}
	m.State.Approvals = out
	delete(m.ApprovalErrors, key)
}
func (m *Client) reconcileApprovals() {
	for id := range m.ApprovalPending {
		found := false
		for _, a := range m.State.Approvals {
			if string(a.ID) == id {
				found = true
				break
			}
		}
		if !found {
			m.clearApprovalPending(id)
			delete(m.ApprovalErrors, id)
		}
	}
}
func (m *Client) interruptApprovals() {
	for id := range m.ApprovalPending {
		m.clearApprovalPending(id)
		m.approvalError(id, tr("Disconnected. Reconnect to try again."))
	}
}
