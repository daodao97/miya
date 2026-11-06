package presentation

import (
	"encoding/json"
	"strings"

	"miya/internal/chat/model"
	chattext "miya/internal/chat/text"
)

// ToolInvocation is the common identity and arguments of an external tool call.
// Native envelopes are interpreted here, never by the MCP service.
type ToolInvocation struct {
	Server, Name string
	Arguments    json.RawMessage
}

func ToolCall(message model.Message) (ToolInvocation, bool) {
	switch strings.ToLower(message.Kind) {
	case "mcptoolcall":
		if message.Item != nil {
			return ToolInvocation{Server: message.Item.Server, Name: message.Item.Tool, Arguments: message.Item.Arguments}, true
		}
	case "claudetool":
		name, input := chattext.ToolCall(message)
		rest, ok := strings.CutPrefix(name, "mcp__")
		if !ok {
			return ToolInvocation{}, false
		}
		server, tool, ok := strings.Cut(rest, "__")
		if !ok {
			return ToolInvocation{}, false
		}
		raw, _ := json.Marshal(input)
		return ToolInvocation{Server: server, Name: tool, Arguments: raw}, true
	}
	return ToolInvocation{}, false
}
