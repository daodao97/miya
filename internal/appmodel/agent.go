package appmodel

import (
	"miya/internal/model"
)

const (
	AgentCodex  = model.AgentCodex
	AgentClaude = model.AgentClaude
)

// agentIconSpec is an agent's icon: an embedded SVG in a color, or a bitmap.
type AgentIconSpec struct {
	SVG, Color string
	PNG        []byte
}
