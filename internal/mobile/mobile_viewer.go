package mobile

import (
	"miya/internal/appmodel"
	"miya/internal/interaction"
	"miya/internal/model"
	"miya/internal/uiruntime"
)

// mobileViewer holds only the selected snapshot. It never starts an agent.
type mobileViewer struct {
	threads      func() []model.Thread
	SetThreads   func([]model.Thread)
	Active       func() int
	SetActive    func(int)
	Revision     func() int
	setRevision  func(int)
	approvals    func() []interaction.Request
	setApprovals func([]interaction.Request)
}

func newMobileViewer() *mobileViewer {
	v := &mobileViewer{}
	v.threads, v.SetThreads = uiruntime.CreateSignal([]model.Thread{})
	v.Active, v.SetActive = uiruntime.CreateSignal(0)
	v.Revision, v.setRevision = uiruntime.CreateSignal(0)
	v.approvals, v.setApprovals = uiruntime.CreateSignal([]interaction.Request{})
	return v
}

func (v *mobileViewer) Current() model.Thread {
	for _, t := range v.threads() {
		if t.ID == v.Active() {
			return t
		}
	}
	return model.Thread{}
}

func (v *mobileViewer) selectThread(t model.Thread) {
	v.SetThreads([]model.Thread{t})
	v.SetActive(t.ID)
}

func (v *mobileViewer) approvalDetail(a interaction.Request) string {
	return appmodel.ApprovalDetail(v.threads(), a)
}
