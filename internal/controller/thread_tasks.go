package controller

import (
	fmt "fmt"
	appmodel "miya/internal/appmodel"
	strings "strings"
)

func (c *Controller) TaskWaitingApproval(t appmodel.Thread) bool {
	for _, a := range c.Approvals() {
		if a.ThreadID == t.ServerID && t.ServerID != "" || strings.HasPrefix(string(a.ID), fmt.Sprintf("\"claude:%d:", t.ID)) {
			return true
		}
	}
	return false
}
