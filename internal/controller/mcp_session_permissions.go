package controller

import (
	"errors"

	codex "miya/internal/agent/codex/native"
	mcpconfig "miya/internal/mcp/config"
	"miya/internal/phone"
	"miya/internal/svcproto"
)

func (c *Controller) RememberedMCPAccess(a codex.Approval) bool {
	return mcpconfig.RememberableMCPAccess(a) && c.mcpSessionAccess[mcpconfig.SessionPermissionKey(a)]
}

func (c *Controller) RespondMCPAlways(a codex.Approval) error {
	if c.Remote != nil {
		if err := c.Remote.Call("respond", svcproto.Respond{ID: a.ID, Remember: true}, nil); err != nil {
			return err
		}
		c.removeRequest(a.ID)
		return nil
	}
	// Resolve from the live request queue; stale cards cannot create a grant.
	for _, pending := range c.Approvals() {
		if !phone.SameJSON(pending.ID, a.ID) {
			continue
		}
		if !mcpconfig.RememberableMCPAccess(pending) {
			return errors.New("Always allow in this conversation is not supported for this request")
		}
		if c.mcpSessionAccess == nil {
			c.mcpSessionAccess = map[string]bool{}
		}
		// Deliberately memory-only: scoped to this thread and backend lifetime.
		c.mcpSessionAccess[mcpconfig.SessionPermissionKey(pending)] = true
		c.Respond(pending, mcpconfig.AccessAccepted())
		return nil
	}
	return errors.New("Request already handled or expired")
}
