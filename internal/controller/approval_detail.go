package controller

import (
	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
)

func (c *Controller) ApprovalDetail(a codex.Approval) string {
	return appmodel.ApprovalDetail(c.Threads(), a)
}

// DescribeApproval is what an approval card shows of a request.
func (c *Controller) DescribeApproval(a codex.Approval) appmodel.ApprovalView {
	return appmodel.DescribeApproval(c.Threads(), a)
}
