package controller

import (
	"miya/internal/codex"
	"miya/internal/model"
)

func (c *Controller) ViewThread() model.Thread        { return c.Current() }
func (c *Controller) ViewRevision() int               { return c.Revision() }
func (c *Controller) ViewApprovals() []codex.Approval { return c.Approvals() }
func (c *Controller) ViewSelectThread(t model.Thread) { c.SelectThread(t) }
