package coordinator

import (
	"miya/internal/model"
)

// Host is what Coordinator needs from the controller
// whose conversations carry the delegated work: the background service's.
// Dispatch may be called from any goroutine; the other methods run on the
// controller's thread.
type Host interface {
	Dispatch(f func())
	// Invalidate redraws after the collaboration state changed.
	Invalidate()

	Thread(id int) model.Thread
	// ThreadByRef finds a conversation by its thread URL.
	ThreadByRef(raw string) (model.Thread, bool)
	ThreadTitle(t model.Thread) string
	NextID() int
	AddThread(project string, t model.Thread)
	Update(id int, f func(*model.Thread))
	// DrainQueue sends a conversation's next queued message, if it can.
	DrainQueue(id int)

	// Submit sends a message and returns its conversation, 0 when it was
	// not started.
	Submit(model.SendRequest) int
	Permission(t model.Thread) string
	// AwaitsUser reports whether a conversation waits for an approval or an
	// answer.
	AwaitsUser(t model.Thread) bool
	// TargetUnavailable reports whether a conversation runs native work that
	// a delegated input must not borrow.
	TargetUnavailable(t model.Thread) bool
	Agent(id string) model.AgentStatus
	// StopRun asks a conversation's agent to stop its turn.
	StopRun(t model.Thread)
	// CodexEpoch is the connected Codex runtime's epoch, false without one.
	CodexEpoch() (string, bool)
	// PrepareRecheck checks that a task's native outcome can be rechecked and
	// returns an async.Reply that does it off the controller's thread.
	PrepareRecheck(caller, id string) (any, error)
}
