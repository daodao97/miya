package controller

import (
	appmodel "miya/internal/appmodel"
	coordinator "miya/internal/delegation/coordinator"
	model "miya/internal/model"
	uiruntime "miya/internal/uiruntime"
)

// collaborationHostOf is the background service's controller as a
// collaborationHost.
type collaborationHostOf struct{ c *Controller }

func (h collaborationHostOf) Dispatch(f func()) { h.c.Dispatch(f) }

func (h collaborationHostOf) Invalidate() { uiruntime.Invalidate() }

func (h collaborationHostOf) Thread(id int) appmodel.Thread { return h.c.ThreadWithID(id) }

func (h collaborationHostOf) ThreadByRef(raw string) (appmodel.Thread, bool) {
	return h.c.ThreadByReference(raw)
}

func (h collaborationHostOf) ThreadTitle(t appmodel.Thread) string { return appmodel.ThreadRefTitle(t) }

func (h collaborationHostOf) NextID() int { return h.c.NextID() }

func (h collaborationHostOf) AddThread(project string, t appmodel.Thread) { h.c.AddThread(project, t) }

func (h collaborationHostOf) Update(id int, f func(*appmodel.Thread)) { h.c.Update(id, f) }

func (h collaborationHostOf) DrainQueue(id int) { h.c.drainQueue(id) }

func (h collaborationHostOf) Submit(req appmodel.SendRequest) int { return h.c.SubmitExpanded(req) }

func (h collaborationHostOf) Permission(t appmodel.Thread) string { return h.c.permissionForThread(t) }

func (h collaborationHostOf) AwaitsUser(t appmodel.Thread) bool { return h.c.ThreadAwaitsUser(t) }

func (h collaborationHostOf) TargetUnavailable(t appmodel.Thread) bool {
	return h.c.managedTargetUnavailable(t)
}

func (h collaborationHostOf) Agent(id string) model.AgentStatus { return h.c.agentStatus(id) }

func (h collaborationHostOf) StopRun(t appmodel.Thread) { h.c.stopAgentRun(t) }

func (h collaborationHostOf) CodexEpoch() (string, bool) {
	if h.c.Client == nil {
		return "", false
	}
	return h.c.Client.RuntimeEpoch(), true
}

func (h collaborationHostOf) PrepareRecheck(caller, id string) (any, error) {
	return h.c.PrepareTaskRecheck(caller, id)
}

// The background service's collaboration runs in package coordinator.
type collaborationState = coordinator.Coordinator

type collaborationHost = coordinator.Host

type ManagedExecution = coordinator.Execution

var errManagedCancelled = coordinator.ErrCancelled

var errManagedPaused = coordinator.ErrPaused

func (c *Controller) resumeCollaboration(ref string) appmodel.Deferred {
	return c.Collaboration.Resume(ref)
}

func NewCollaboration(c *Controller, dir string) *collaborationState {
	return coordinator.New(collaborationHostOf{c}, c.parentContext(), dir)
}
