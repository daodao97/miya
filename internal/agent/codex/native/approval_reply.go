package codex

import (
	"encoding/json"
	"strings"

	"miya/internal/interaction"
	"miya/internal/interaction/legacy"
)

func ApprovalResult(a Approval, allow bool) any { return legacy.ApprovalResult(a, allow) }

func IsMiyaApproval(a Approval) bool { return strings.Contains(string(a.ID), "\"miya:") }

// ValidateForm retains the native client's public helper while the form contract
// lives in the neutral interaction package.
func ValidateForm(raw json.RawMessage, content map[string]any) error {
	return interaction.ValidateForm(raw, content)
}

// EncodeResponse translates shared user intent to the app-server reply shape.
func EncodeResponse(a Approval, r interaction.Response) any { return legacy.EncodeResponse(a, r) }
