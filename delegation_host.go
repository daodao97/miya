package main

import (
	"miya/internal/delegation/coordinator"
	"miya/internal/model"
)

// collaborationHostOf is the background service's controller as a
// collaborationHost.
type collaborationHostOf struct{ c *chatController }

func (h collaborationHostOf) Dispatch(f func()) { h.c.dispatch(f) }

func (h collaborationHostOf) Invalidate() { invalidate() }

func (h collaborationHostOf) Thread(id int) Thread { return h.c.threadWithID(id) }

func (h collaborationHostOf) ThreadByRef(raw string) (Thread, bool) {
	return h.c.threadByReference(raw)
}

func (h collaborationHostOf) ThreadTitle(t Thread) string { return threadRefTitle(t) }

func (h collaborationHostOf) NextID() int { return h.c.nextID() }

func (h collaborationHostOf) AddThread(project string, t Thread) { h.c.addThread(project, t) }

func (h collaborationHostOf) Update(id int, f func(*Thread)) { h.c.update(id, f) }

func (h collaborationHostOf) DrainQueue(id int) { h.c.drainQueue(id) }

func (h collaborationHostOf) Submit(req SendRequest) int { return h.c.submitExpanded(req) }

func (h collaborationHostOf) Permission(t Thread) string { return h.c.permissionForThread(t) }

func (h collaborationHostOf) AwaitsUser(t Thread) bool { return h.c.threadAwaitsUser(t) }

func (h collaborationHostOf) TargetUnavailable(t Thread) bool { return h.c.managedTargetUnavailable(t) }

func (h collaborationHostOf) Agent(id string) model.AgentStatus { return h.c.agentStatus(id) }

func (h collaborationHostOf) StopRun(t Thread) { h.c.stopAgentRun(t) }

func (h collaborationHostOf) CodexEpoch() (string, bool) {
	if h.c.client == nil {
		return "", false
	}
	return h.c.client.RuntimeEpoch(), true
}

func (h collaborationHostOf) PrepareRecheck(caller, id string) (any, error) {
	return h.c.prepareTaskRecheck(caller, id)
}

// The background service's collaboration runs in package coordinator.
type (
	collaborationState = coordinator.Coordinator
	collaborationHost  = coordinator.Host
	managedExecution   = coordinator.Execution
)

var (
	errManagedCancelled = coordinator.ErrCancelled
	errManagedPaused    = coordinator.ErrPaused
)

func (c *chatController) resumeCollaboration(ref string) deferred { return c.collaboration.Resume(ref) }

func newCollaboration(c *chatController, dir string) *collaborationState {
	return coordinator.New(collaborationHostOf{c}, c.parentContext(), dir)
}
