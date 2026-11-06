package controller

import (
	errors "errors"
	appmodel "miya/internal/appmodel"
	codex "miya/internal/codex"
)

func (c *Controller) RememberedMCPAccess(a codex.Approval) bool {
	return appmodel.RememberableMCPAccess(a) && c.mcpSessionAccess[appmodel.McpSessionPermissionKey(a)]
}

func (c *Controller) RespondMCPAlways(a codex.Approval) error {
	if c.Remote != nil {
		if err := c.Remote.Call("respond", appmodel.SvcRespond{ID: a.ID, Remember: true}, nil); err != nil {
			return err
		}
		c.removeRequest(a.ID)
		return nil
	}
	// Resolve from the live request queue; stale cards cannot create a grant.
	for _, pending := range c.Approvals() {
		if !appmodel.SameJSON(pending.ID, a.ID) {
			continue
		}
		if !appmodel.RememberableMCPAccess(pending) {
			return errors.New("Always allow in this conversation is not supported for this request")
		}
		if c.mcpSessionAccess == nil {
			c.mcpSessionAccess = map[string]bool{}
		}
		// Deliberately memory-only: scoped to this thread and backend lifetime.
		c.mcpSessionAccess[appmodel.McpSessionPermissionKey(pending)] = true
		c.Respond(pending, appmodel.McpAccessAccepted())
		return nil
	}
	return errors.New("Request already handled or expired")
}
