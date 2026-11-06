package controller

import (
	"miya/internal/appmodel"
	"miya/internal/codex"
)

func (c *Controller) ViewThread() appmodel.Thread        { return c.Current() }
func (c *Controller) ViewRevision() int                  { return c.Revision() }
func (c *Controller) ViewApprovals() []codex.Approval    { return c.Approvals() }
func (c *Controller) ViewSelectThread(t appmodel.Thread) { c.SelectThread(t) }
