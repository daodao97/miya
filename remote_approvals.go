package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"miya/internal/remote"
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
