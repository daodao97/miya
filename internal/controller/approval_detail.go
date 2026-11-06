package controller

import (
	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
)

func (c *Controller) ApprovalDetail(a codex.Approval) string {
	return appmodel.ApprovalDetail(c.Threads(), a)
}
