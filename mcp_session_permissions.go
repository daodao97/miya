package main

import (
	"errors"

	"miya/internal/codex"
)

func (c *chatController) rememberedMCPAccess(a codex.Approval) bool {
	return rememberableMCPAccess(a) && c.mcpSessionAccess[mcpSessionPermissionKey(a)]
}

func (c *chatController) respondMCPAlways(a codex.Approval) error {
	if c.remote != nil {
		if err := c.remote.Call("respond", svcRespond{ID: a.ID, Remember: true}, nil); err != nil {
			return err
		}
		c.removeRequest(a.ID)
		return nil
	}
	// Resolve from the live request queue; stale cards cannot create a grant.
	for _, pending := range c.approvals() {
		if !sameJSON(pending.ID, a.ID) {
			continue
		}
		if !rememberableMCPAccess(pending) {
			return errors.New("Always allow in this conversation is not supported for this request")
		}
		if c.mcpSessionAccess == nil {
			c.mcpSessionAccess = map[string]bool{}
		}
		// Deliberately memory-only: scoped to this thread and backend lifetime.
		c.mcpSessionAccess[mcpSessionPermissionKey(pending)] = true
		c.respond(pending, mcpAccessAccepted())
		return nil
	}
	return errors.New("Request already handled or expired")
}
