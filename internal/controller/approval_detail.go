package controller

import (
	"miya/internal/appmodel"
	"miya/internal/codex"
)

func (c *Controller) ApprovalDetail(a codex.Approval) string {
	return appmodel.ApprovalDetail(c.Threads(), a)
}
