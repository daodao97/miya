package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	mcpproto "miya/internal/mcp/protocol"
	"miya/internal/statecard"
)

func init() {
	text := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	items := func(most int, description string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": most, "description": description}
	}
	Register(statusTool, false, mcpproto.Tool{
		Name:        "update_status",
		Description: "Write this conversation's state card: what it is for, what is done, what remains and whether it waits for the person. Call it before ending a turn that changed where the work stands. The card replaces the earlier one, so give the whole state each time. Miya keeps it outside the conversation and shows it to other conversations through list_threads and thread_status, as your own account that Miya has not verified. Keep it to a few short lines; point to commits, files and session links in references instead of copying them.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"purpose":     text(fmt.Sprintf("One sentence on what this conversation is for (maximum %d characters).", statecard.MaxText)),
			"done":        items(statecard.MaxItems, "What is finished, one short line each."),
			"remaining":   items(statecard.MaxItems, "What is left to do, one short line each. Leave it out when nothing remains."),
			"waiting_for": text("The question or decision you wait for from the person. Leave it out when you wait for nothing."),
			"checked":     items(statecard.MaxItems, "What you checked yourself and how, such as a test command and its result. Miya keeps it as your own account."),
			"references":  items(statecard.MaxReferences, "Commits, pull requests, files and session links that hold the detail."),
		}, "required": []string{"purpose"}, "additionalProperties": false},
		Annotations: map[string]any{"readOnlyHint": false, "idempotentHint": true, "openWorldHint": false},
	})
}

func statusTool(s *Server, name string, raw json.RawMessage) (string, error) {
	var args mcpproto.StatusArgs
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return "", errors.New("Invalid status arguments: " + err.Error())
	}
	var text string
	err := s.CallService("mcpStateCard", mcpproto.Request{Tool: name, Arguments: raw}, &text)
	return text, err
}
