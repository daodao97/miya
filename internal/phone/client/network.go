package client

import (
	"time"
)

const pathProbeTimeout = 20 * time.Second

type mobileNetworkPath struct {
	known  bool
	online bool
	iface  string
}

func (m *Client) NetworkPathChanged(online bool, iface string) {
	prev := m.net
	m.net = mobileNetworkPath{known: true, online: online, iface: iface}
	if !prev.known || !online || prev.online == online && prev.iface == iface {
		return
	}
	if m.Cancel == nil {
		return // Not connecting: backgrounded, or no desktop paired.
	}
	if !m.Connected {
		m.retryNow()
		return
	}
	if conn := m.Conn; conn != nil {
		// Each received frame resets the deadline in read.
		_ = conn.SetReadDeadline(time.Now().Add(pathProbeTimeout))
	}
}

func (m *Client) retryNow() bool {
	if m.Cancel == nil || m.Connected || m.Wake == nil {
		return false
	}
	select {
	case m.Wake <- struct{}{}:
	default: // A wake is already pending.
	}
	return true
}

func (m *Client) disconnectedSendError() string {
	if m.retryNow() {
		return tr("Not connected to desktop. Reconnecting…")
	}
	return tr("Not connected to desktop")
}
