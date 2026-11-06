package server

import (
	"miya/internal/chat/model"
	svcproto "miya/internal/service/protocol"
)

// Host binds transport and lifecycle to the application's executor. The
// service owns peers, replication, timeouts and idle shutdown; business
// operations and their state remain in their owning domains.
type Host interface {
	SnapshotState() svcproto.State
	ThreadWithID(int) model.Thread
	ServiceRequest(svcproto.Request) (any, error)
	ServiceBusy() bool
	ServiceKeepAlive() bool
	ServiceStop()
	ServicePause()
}
