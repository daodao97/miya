package appmodel

import (
	json "encoding/json"
)

// The authenticated desktop/phone API shares task admission and mutation with
// MCP. Its user principal may inspect/manage tasks across conversations. MCP
// tool handlers stay on the separate strict caller-evidence route.
type CollaborationRequest struct {
	Operation string          `json:"operation"`
	From      string          `json:"from,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}
