package controller

import (
	"errors"

	"miya/internal/notification"
)

// NotificationPolicyOp reads or changes how phone notices are delivered:
// quiet hours and the rules learned from how notices were received. The
// background service owns them.
func (c *Controller) NotificationPolicyOp(op string, r notification.PolicyRequest) (any, error) {
	if c.Push == nil {
		return nil, errors.New("Notification rules require the Miya background service")
	}
	switch op {
	case "notificationPolicy":
	case "notificationQuietHours":
		if r.Quiet == nil {
			return nil, errors.New("Quiet hours need start and end times as HH:MM")
		}
		if err := c.Push.SetQuietHours(*r.Quiet); err != nil {
			return nil, err
		}
	case "notificationRuleForget":
		if err := c.Push.ForgetRule(r.Forget); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("Unknown notification operation")
	}
	return c.Push.Policy(), nil
}
