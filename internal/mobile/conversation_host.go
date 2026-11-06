package mobile

import (
	"miya/internal/appmodel"
	"miya/internal/codex"
)

func (v *mobileViewer) ViewThread() appmodel.Thread        { return v.Current() }
func (v *mobileViewer) ViewRevision() int                  { return v.Revision() }
func (v *mobileViewer) ViewApprovals() []codex.Approval    { return v.approvals() }
func (v *mobileViewer) ViewSelectThread(t appmodel.Thread) { v.selectThread(t) }
