package controller

import (
	appmodel "miya/internal/appmodel"
	codex "miya/internal/codex"
)

func (c *Controller) ApprovalDetail(a codex.Approval) string {
	return appmodel.ApprovalDetail(c.Threads(), a)
}
