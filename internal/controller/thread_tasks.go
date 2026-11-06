package controller

import (
	"fmt"
	"strings"

	"miya/internal/model"
)

func (c *Controller) TaskWaitingApproval(t model.Thread) bool {
	for _, a := range c.Approvals() {
		if a.ThreadID == t.ServerID && t.ServerID != "" || strings.HasPrefix(string(a.ID), fmt.Sprintf("\"claude:%d:", t.ID)) {
			return true
		}
	}
	return false
}
