package main

import (
	"miya/internal/codex"
)

// mobileViewer holds only the selected snapshot. It never starts an agent.
type mobileViewer struct {
	threads      func() []Thread
	setThreads   func([]Thread)
	active       func() int
	setActive    func(int)
	revision     func() int
	setRevision  func(int)
	approvals    func() []codex.Approval
	setApprovals func([]codex.Approval)
}

func newMobileViewer() *mobileViewer {
	v := &mobileViewer{}
	v.threads, v.setThreads = createSignal([]Thread{})
	v.active, v.setActive = createSignal(0)
	v.revision, v.setRevision = createSignal(0)
	v.approvals, v.setApprovals = createSignal([]codex.Approval{})
	return v
}
func (v *mobileViewer) current() Thread {
	for _, t := range v.threads() {
		if t.ID == v.active() {
			return t
		}
	}
	return Thread{}
}
func (v *mobileViewer) selectThread(t Thread)                  { v.setThreads([]Thread{t}); v.setActive(t.ID) }
func (v *mobileViewer) approvalDetail(a codex.Approval) string { return approvalDetail(v.threads(), a) }
