package codex

import (
	"encoding/json"
	"strings"

	"miya/internal/interaction"
)

func ApprovalResult(a Approval, allow bool) any {
	if a.Method == "item/permissions/requestApproval" {
		var permissions any = map[string]any{}
		if allow && len(a.Permissions) > 0 {
			_ = json.Unmarshal(a.Permissions, &permissions)
		}
		return map[string]any{"permissions": permissions, "scope": "turn"}
	}
	decision := "decline"
	if allow {
		decision = "accept"
	}
	return map[string]string{"decision": decision}
}

func IsMiyaApproval(a Approval) bool { return strings.Contains(string(a.ID), "\"miya:") }

// ValidateForm retains the native client's public helper while the form contract
// lives in the neutral interaction package.
func ValidateForm(raw json.RawMessage, content map[string]any) error {
	return interaction.ValidateForm(raw, content)
}

// EncodeResponse translates shared user intent to the app-server reply shape.
func EncodeResponse(a Approval, r interaction.Response) any {
	switch a.Kind() {
	case interaction.Questions:
		answers := map[string]any{}
		if r.Action != interaction.Skip {
			for id, text := range r.Answers {
				answers[id] = map[string]any{"answers": []string{text}}
			}
		}
		return map[string]any{"answers": answers}
	case interaction.Form:
		action := "accept"
		if r.Action == interaction.Decline {
			action = "decline"
		} else if r.Action == interaction.Cancel {
			action = "cancel"
		}
		out := map[string]any{"action": action}
		if r.Content != nil {
			out["content"] = r.Content
		}
		return out
	default:
		return ApprovalResult(a, r.Action == interaction.Allow)
	}
}
