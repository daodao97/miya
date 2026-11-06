// Package access owns MCP application-access policy and conversation-lifetime
// grants. Grants are deliberately memory-only and require a live request.
package access

import (
	"encoding/json"
	"errors"

	"miya/internal/interaction"
	"miya/internal/textutil"
)

type Host interface {
	Forward(json.RawMessage) (handled bool, err error)
	Pending() []interaction.Request
	Respond(interaction.Request, any)
}

type Grants struct{ allowed map[string]bool }

func (g *Grants) Remembered(request interaction.Request) bool {
	return Rememberable(request) && g.allowed[Key(request)]
}
func (g *Grants) Confirm(request interaction.Request, host Host) error {
	if forwarded, err := host.Forward(request.ID); forwarded {
		return err
	}
	// Trust the current queue, never the caller's submitted scope or message.
	for _, pending := range host.Pending() {
		if !textutil.SameJSON(pending.ID, request.ID) {
			continue
		}
		if !Rememberable(pending) {
			return errors.New("Always allow in this conversation is not supported for this request")
		}
		if g.allowed == nil {
			g.allowed = map[string]bool{}
		}
		g.allowed[Key(pending)] = true
		host.Respond(pending, Accepted())
		return nil
	}
	return errors.New("Request already handled or expired")
}
