package codex

import (
	"encoding/json"
	"fmt"
	"strings"
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

func ValidateForm(raw json.RawMessage, content map[string]any) error {
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil {
		return err
	}
	for key := range shape {
		switch key {
		case "type", "properties", "required", "title", "description", "additionalProperties", "$schema":
		default:
			return fmt.Errorf("Unsupported form constraint: %s; decline or cancel", key)
		}
	}
	var properties map[string]map[string]json.RawMessage
	if err := json.Unmarshal(shape["properties"], &properties); err != nil {
		return fmt.Errorf("Unsupported form properties")
	}
	for _, property := range properties {
		for key := range property {
			switch key {
			case "type", "enum", "title", "description", "default":
			default:
				return fmt.Errorf("Unsupported field constraint: %s; decline or cancel", key)
			}
		}
	}

	var schema struct {
		Type       string   `json:"type"`
		Required   []string `json:"required"`
		Properties map[string]struct {
			Type string `json:"type"`
			Enum []any  `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return fmt.Errorf("Invalid form schema: %w", err)
	}
	if schema.Type != "object" {
		return fmt.Errorf("Unsupported form schema; decline or cancel this request")
	}
	for _, key := range schema.Required {
		if _, ok := content[key]; !ok {
			return fmt.Errorf("Missing required field: %s", key)
		}
	}
	for key, value := range content {
		field, ok := schema.Properties[key]
		if !ok {
			return fmt.Errorf("Unknown field: %s", key)
		}
		valid := false
		switch field.Type {
		case "string":
			_, valid = value.(string)
		case "boolean":
			_, valid = value.(bool)
		case "number":
			_, valid = value.(float64)
		case "integer":
			n, ok := value.(float64)
			valid = ok && n == float64(int64(n))
		default:
			return fmt.Errorf("Unsupported field type %s; decline or cancel", field.Type)
		}
		if !valid {
			return fmt.Errorf("%s must be %s", key, field.Type)
		}
		if len(field.Enum) > 0 {
			found := false
			for _, option := range field.Enum {
				if fmt.Sprint(option) == fmt.Sprint(value) {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("%s must match one of the offered values", key)
			}
		}
	}
	return nil
}

// Miya's own requests to the person, shown as approval cards in a
// conversation: their ids start with miya: and Miya answers them itself.
