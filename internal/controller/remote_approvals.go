package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"miya/internal/codex"
	"miya/internal/phone"
	"miya/internal/phoneapi"
	"miya/internal/remote"
)

func (c *Controller) PhoneApprovalResponse(r remote.Request) error {
	for _, a := range c.Approvals() {
		if !phone.SameJSON(a.ID, r.Approval) {
			continue
		}
		if r.ApprovalVersion != "" && r.ApprovalVersion != phoneapi.ApprovalFingerprint(a) {
			return errors.New("Request changed. Confirm again.")
		}
		if r.Remember {
			return c.RespondMCPAlways(a)
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
					if err := codex.ValidateForm(a.RequestedSchema, v.Content); err != nil {
						return err
					}
				}
			case "decline", "cancel":
			default:
				return errors.New("Invalid form action")
			}
			result = v
		default:
			result = codex.ApprovalResult(a, r.Allow)
		}
		c.Respond(a, result)
		return nil
	}
	return fmt.Errorf("Request already handled or expired")
}
