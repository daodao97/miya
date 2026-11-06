package mobile

import (
	"time"

	"github.com/egoist/mygo"
)

// pathProbeTimeout bounds the wait for the desktop's next frame after the
// phone's network path changes. The desktop pings every 15 seconds, so a
// tunnel that survived the change (WireGuard roams) answers in time, and a
// dead one is noticed well before the normal 45 second read deadline.
const pathProbeTimeout = 20 * time.Second

// mobileNetworkPath is the last network path the phone saw.
type mobileNetworkPath struct {
	known  bool
	online bool
	iface  string
}

// watchNetwork follows the system network path, falling back to the plain
// online state where detailed paths are unsupported. Call on the main thread
// after the app is ready.
func (m *App) watchNetwork() {
	if _, err := mygo.Network.OnPathChanged(func(s mygo.NetworkPathStatus) {
		if s.Known {
			m.NetworkPathChanged(s.Online, s.Interface)
		}
	}); err == nil {
		return
	}
	s := mygo.Network.Status()
	m.NetworkPathChanged(s.Online, "")
	mygo.Network.OnChanged(func(s mygo.NetworkStatus) { m.NetworkPathChanged(s.Online, "") })
}

// networkPathChanged runs on the main thread. The first report only records
// the path. Afterwards, coming online or moving to another interface either
// retries a waiting connection at once, or makes the live one prove itself
// within pathProbeTimeout instead of tearing it down.
func (m *App) NetworkPathChanged(online bool, iface string) {
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

// retryNow ends the running connection's reconnect wait, reporting whether
// a reconnect is under way.
func (m *App) retryNow() bool {
	if m.Cancel == nil || m.Connected || m.Wake == nil {
		return false
	}
	select {
	case m.Wake <- struct{}{}:
	default: // A wake is already pending.
	}
	return true
}

// disconnectedSendError explains a send without a connection, retrying at
// once when a desktop is paired: the user wants it now, not after backoff.
func (m *App) disconnectedSendError() string {
	if m.retryNow() {
		return tr("Not connected to desktop. Reconnecting…")
	}
	return tr("Not connected to desktop")
}
