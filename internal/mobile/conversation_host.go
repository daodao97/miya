package mobile

import (
	"miya/internal/chat/model"
	"miya/internal/interaction"
)

func (v *mobileViewer) ViewThread() model.Thread             { return v.Current() }
func (v *mobileViewer) ViewRevision() int                    { return v.Revision() }
func (v *mobileViewer) ViewApprovals() []interaction.Request { return v.approvals() }
func (v *mobileViewer) ViewSelectThread(t model.Thread)      { v.selectThread(t) }
