package mcp

import (
	"encoding/json"
	"fmt"
	"time"

	dg "miya/internal/delegation"
	mcpproto "miya/internal/mcp/protocol"
)

func init() {
	Register(followUpTool, false, mcpproto.Tool{
		Name:        "follow_up",
		Description: "Schedule a one-time follow-up in this conversation. Use it when the user's current work needs a later check. Miya delivers the note at or after the chosen time once this conversation is idle. The timer survives a background service restart. At most three outstanding follow-ups per conversation, at most 30 days ahead.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"at":   map[string]any{"type": "string", "format": "date-time", "description": "Due time in RFC 3339 format, including the timezone offset."},
			"note": map[string]any{"type": "string", "maxLength": dg.MaxFollowUpNote, "description": "One short reminder of what to check and why."},
		}, "required": []string{"at", "note"}, "additionalProperties": false},
		Annotations: map[string]any{"readOnlyHint": false, "idempotentHint": true, "openWorldHint": false},
	})
}

func followUpTool(s *Server, name string, raw json.RawMessage) (string, error) {
	var out mcpproto.FollowUpResult
	if err := s.CallService("mcpFollowUp", mcpproto.Request{Tool: name, Arguments: raw}, &out); err != nil {
		return "", err
	}
	return fmt.Sprintf("Follow-up %s is %s. Due at %s.", out.ID, out.State, out.At.Format(time.RFC3339)), nil
}
