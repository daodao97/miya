package main

import "miya/internal/model"

// collaborationHost is what collaborationState needs from the controller
// whose conversations carry the delegated work: the background service's.
// Dispatch may be called from any goroutine; the other methods run on the
// controller's thread.
type collaborationHost interface {
	Dispatch(f func())
	// Invalidate redraws after the collaboration state changed.
	Invalidate()

	Thread(id int) Thread
	// ThreadByRef finds a conversation by its thread URL.
	ThreadByRef(raw string) (Thread, bool)
	ThreadTitle(t Thread) string
	NextID() int
	AddThread(project string, t Thread)
	Update(id int, f func(*Thread))
	// DrainQueue sends a conversation's next queued message, if it can.
	DrainQueue(id int)

	// Submit sends a message and returns its conversation, 0 when it was
	// not started.
	Submit(SendRequest) int
	Permission(t Thread) string
	// AwaitsUser reports whether a conversation waits for an approval or an
	// answer.
	AwaitsUser(t Thread) bool
	// TargetUnavailable reports whether a conversation runs native work that
	// a delegated input must not borrow.
	TargetUnavailable(t Thread) bool
	Agent(id string) model.AgentStatus
	// StopRun asks a conversation's agent to stop its turn.
	StopRun(t Thread)
	// CodexEpoch is the connected Codex runtime's epoch, false without one.
	CodexEpoch() (string, bool)
	// PrepareRecheck checks that a task's native outcome can be rechecked and
	// returns an async.Reply that does it off the controller's thread.
	PrepareRecheck(caller, id string) (any, error)
}

// collaborationHostOf is the background service's controller as a
// collaborationHost.
type collaborationHostOf struct{ c *chatController }

func (h collaborationHostOf) Dispatch(f func())    { h.c.dispatch(f) }
func (h collaborationHostOf) Invalidate()          { invalidate() }
func (h collaborationHostOf) Thread(id int) Thread { return h.c.threadWithID(id) }
func (h collaborationHostOf) ThreadByRef(raw string) (Thread, bool) {
	return h.c.threadByReference(raw)
}
func (h collaborationHostOf) ThreadTitle(t Thread) string        { return threadRefTitle(t) }
func (h collaborationHostOf) NextID() int                        { return h.c.nextID() }
func (h collaborationHostOf) AddThread(project string, t Thread) { h.c.addThread(project, t) }
func (h collaborationHostOf) Update(id int, f func(*Thread))     { h.c.update(id, f) }
func (h collaborationHostOf) DrainQueue(id int)                  { h.c.drainQueue(id) }
func (h collaborationHostOf) Submit(req SendRequest) int         { return h.c.submitExpanded(req) }
func (h collaborationHostOf) Permission(t Thread) string         { return h.c.permissionForThread(t) }
func (h collaborationHostOf) AwaitsUser(t Thread) bool           { return h.c.threadAwaitsUser(t) }
func (h collaborationHostOf) TargetUnavailable(t Thread) bool    { return h.c.managedTargetUnavailable(t) }
func (h collaborationHostOf) Agent(id string) model.AgentStatus  { return h.c.agentStatus(id) }
func (h collaborationHostOf) StopRun(t Thread)                   { h.c.stopAgentRun(t) }

func (h collaborationHostOf) CodexEpoch() (string, bool) {
	if h.c.client == nil {
		return "", false
	}
	return h.c.client.RuntimeEpoch(), true
}

func (h collaborationHostOf) PrepareRecheck(caller, id string) (any, error) {
	return h.c.prepareTaskRecheck(caller, id)
}
