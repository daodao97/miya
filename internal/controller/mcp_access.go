package controller

import (
	"encoding/json"

	"miya/internal/interaction"
	"miya/internal/mcp/access"
	"miya/internal/svcproto"
)

func (c *Controller) RememberedMCPAccess(a interaction.Request) bool {
	return c.mcpSessionAccess.Remembered(a)
}
func (c *Controller) RespondMCPAlways(a interaction.Request) error {
	return c.mcpSessionAccess.Confirm(a, mcpAccessHost{c})
}

type mcpAccessHost struct{ *Controller }

func (h mcpAccessHost) Pending() []interaction.Request { return h.Approvals() }
func (h mcpAccessHost) Forward(id json.RawMessage) (bool, error) {
	if h.Remote == nil {
		return false, nil
	}
	if err := h.Remote.Call("respond", svcproto.Respond{ID: id, Remember: true}, nil); err != nil {
		return true, err
	}
	h.removeRequest(id)
	return true, nil
}

var _ access.Host = mcpAccessHost{}
