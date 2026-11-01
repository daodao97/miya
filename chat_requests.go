package main

import (
	"encoding/json"
	"fmt"
	"miya/internal/codex"
	"strings"
)

func (c *chatController) removeRequest(id json.RawMessage) {
	out := []codex.Approval{}
	for _, v := range c.approvals() {
		if string(v.ID) != string(id) {
			out = append(out, v)
		}
	}
	c.setApprovals(out)
}
func (c *chatController) serverRequest(f codex.Frame) {
	switch f.Method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval", "item/tool/requestUserInput", "mcpServer/elicitation/request":
		var a codex.Approval
		if json.Unmarshal(f.Params, &a) != nil {
			go c.client.Reject(f.ID, "Malformed server request")
			return
		}
		a.ID = f.ID
		a.Method = f.Method
		a.Payload = f.Params
		for _, v := range c.approvals() {
			if string(v.ID) == string(a.ID) {
				return
			}
		}
		if c.client != nil && c.rememberedMCPAccess(a) {
			client := c.client
			go func() {
				if err := client.Reply(a.ID, mcpAccessAccepted()); err != nil {
					c.dispatch(func() { c.setStatus("会话授权回复失败：" + err.Error()) })
				}
			}()
			return
		}
		c.setApprovals(append(append([]codex.Approval(nil), c.approvals()...), a))
	default:
		client := c.client
		go client.Reject(f.ID, "This client does not yet support "+f.Method)
		c.setStatus("Unsupported server request: " + f.Method)
	}
}
func (c *chatController) respond(a codex.Approval, result any) {
	if c.remote != nil {
		c.removeRequest(a.ID)
		c.forward("respond", svcRespond{ID: a.ID, Result: result})
		return
	}
	// A stale card may have been resolved by interruption while the UI was open.
	found := false
	for _, v := range c.approvals() {
		if string(v.ID) == string(a.ID) {
			found = true
		}
	}
	if !found {
		return
	}
	if isMiyaApproval(a) {
		c.respondMiya(a, result)
		return
	}
	approvalOwner(a).Respond(c, a, result)
}
func approvalResult(a codex.Approval, allow bool) any {
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
func (c *chatController) approvalDetail(a codex.Approval) string {
	text := a.Reason + "\n" + a.Command
	if a.Cwd != "" {
		text += "\nDirectory: " + a.Cwd
	}
	if a.GrantRoot != "" {
		text += "\nRequested root: " + a.GrantRoot
	}
	if len(a.Permissions) > 0 {
		text += "\n" + jsonText(a.Permissions)
	}
	for _, t := range c.threads() {
		if t.ServerID == a.ThreadID {
			text = "Thread: " + t.Title + "\n" + text
			if a.Method == "item/fileChange/requestApproval" {
				for _, m := range t.Messages {
					if m.ItemID == a.ItemID {
						text += "\n" + m.Content
					}
				}
			}
		}
	}
	return strings.TrimSpace(text)
}

func validateForm(raw json.RawMessage, content map[string]any) error {
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
