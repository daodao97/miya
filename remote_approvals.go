package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"miya/internal/codex"
	"miya/internal/remote"
	"miya/internal/textutil"
)

func (c *chatController) phoneApprovalResponse(r remote.Request) error {
	for _, a := range c.approvals() {
		if !sameJSON(a.ID, r.Approval) {
			continue
		}
		if r.ApprovalVersion != "" && r.ApprovalVersion != approvalFingerprint(a) {
			return errors.New("Request changed. Confirm again.")
		}
		if r.Remember {
			return c.respondMCPAlways(a)
		}
		var result any
		switch a.Method {
		case "item/tool/requestUserInput":
			var v struct {
				Answers map[string]struct {
					Answers []string `json:"answers"`
				} `json:"answers"`
			}
			if json.Unmarshal(r.Result, &v) != nil || v.Answers == nil {
				return errors.New("Invalid answer")
			}
			// An explicitly empty answer map means Skip, as on desktop.
			if len(v.Answers) > 0 {
				if len(v.Answers) != len(a.Questions) {
					return errors.New("Answer all questions")
				}
				for _, q := range a.Questions {
					answer, ok := v.Answers[q.ID]
					if !ok || len(answer.Answers) != 1 || strings.TrimSpace(answer.Answers[0]) == "" {
						return errors.New("Answer all questions")
					}
				}
			}
			result = v
		case "mcpServer/elicitation/request":
			var v struct {
				Action  string         `json:"action"`
				Content map[string]any `json:"content,omitempty"`
			}
			if json.Unmarshal(r.Result, &v) != nil {
				return errors.New("Invalid form response")
			}
			switch v.Action {
			case "accept":
				if a.Mode != "url" {
					if v.Content == nil {
						return errors.New("Complete the form")
					}
					if err := validateForm(a.RequestedSchema, v.Content); err != nil {
						return err
					}
				}
			case "decline", "cancel":
			default:
				return errors.New("Invalid form action")
			}
			result = v
		default:
			result = approvalResult(a, r.Allow)
		}
		c.respond(a, result)
		return nil
	}
	return fmt.Errorf("Request already handled or expired")
}

// validateMobileForm uses exactly the desktop form contract. The UI renders
// its supported primitive fields; validation remains authoritative here.
func validateMobileForm(a codex.Approval, fields map[string]string) (map[string]any, error) {
	var schema phoneFormSchema
	if err := json.Unmarshal(a.RequestedSchema, &schema); err != nil {
		return nil, err
	}
	content := map[string]any{}
	for key, value := range fields {
		field, ok := schema.Properties[key]
		if !ok {
			continue
		}
		if value == "" {
			continue
		}
		if len(field.Enum) > 0 || field.Type != "string" {
			var decoded any
			if err := json.Unmarshal([]byte(value), &decoded); err != nil {
				return nil, fmt.Errorf("Invalid value for %s", textutil.FirstNonEmpty(field.Title, key))
			}
			content[key] = decoded
		} else {
			content[key] = value
		}
	}
	if err := validateForm(a.RequestedSchema, content); err != nil {
		return nil, err
	}
	return content, nil
}

func approvalFingerprint(a codex.Approval) string {
	data, _ := json.Marshal(a)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
