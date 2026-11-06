package mobile

import (
	"miya/internal/codex"
	"miya/internal/model"
)

func (v *mobileViewer) ViewThread() model.Thread        { return v.Current() }
func (v *mobileViewer) ViewRevision() int               { return v.Revision() }
func (v *mobileViewer) ViewApprovals() []codex.Approval { return v.approvals() }
func (v *mobileViewer) ViewSelectThread(t model.Thread) { v.selectThread(t) }
