package controller

import (
	"strings"
	"testing"

	svcproto "miya/internal/service/protocol"
)

// A window reaches the automations through the service: each automation
// operation must be forwarded there, or a window gets "unknown operation"
// while tests that call AutomationOp directly pass.
func TestServiceForwardsEveryAutomationOp(t *testing.T) {
	c := &Controller{}
	for op := range automationOps {
		_, err := c.ServiceRequest(svcproto.Request{Op: op})
		if err == nil || strings.Contains(err.Error(), "unknown operation") {
			t.Errorf("%s is not forwarded to the automations: %v", op, err)
		}
	}
}
