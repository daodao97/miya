package client

import (
	"miya/internal/remote"
)

func (m *Client) queueAction(op string, id int) {
	if !m.CanSend() {
		m.SendErr = m.disconnectedSendError()
		return
	}
	r := m.ThreadRequest(op, m.Open)
	r.QueuedID = id
	m.sendPhoneAction(r)
}

func (m *Client) sendPhoneAction(r remote.Request) {
	conn, gen := m.Conn, m.Generation
	if !m.CanSend() {
		m.SendErr = m.disconnectedSendError()
		return
	}
	go func() {
		if err := m.write(conn, r); err != nil {
			m.onMain(gen, func() { m.SendErr = tr("Action failed: ") + err.Error() })
		}
	}()
}
