package mobile

import (
	appmodel "miya/internal/appmodel"
	codex "miya/internal/codex"
	uiruntime "miya/internal/uiruntime"
)

// mobileViewer holds only the selected snapshot. It never starts an agent.
type mobileViewer struct {
	threads      func() []appmodel.Thread
	SetThreads   func([]appmodel.Thread)
	Active       func() int
	SetActive    func(int)
	Revision     func() int
	setRevision  func(int)
	approvals    func() []codex.Approval
	setApprovals func([]codex.Approval)
}

func newMobileViewer() *mobileViewer {
	v := &mobileViewer{}
	v.threads, v.SetThreads = uiruntime.CreateSignal([]appmodel.Thread{})
	v.Active, v.SetActive = uiruntime.CreateSignal(0)
	v.Revision, v.setRevision = uiruntime.CreateSignal(0)
	v.approvals, v.setApprovals = uiruntime.CreateSignal([]codex.Approval{})
	return v
}

func (v *mobileViewer) Current() appmodel.Thread {
	for _, t := range v.threads() {
		if t.ID == v.Active() {
			return t
		}
	}
	return appmodel.Thread{}
}

func (v *mobileViewer) selectThread(t appmodel.Thread) {
	v.SetThreads([]appmodel.Thread{t})
	v.SetActive(t.ID)
}

func (v *mobileViewer) approvalDetail(a codex.Approval) string {
	return appmodel.ApprovalDetail(v.threads(), a)
}
