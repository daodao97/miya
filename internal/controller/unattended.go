package controller

import (
	"encoding/json"

	"miya/internal/agent"
	codex "miya/internal/agent/codex/native"
	"miya/internal/automation"
	"miya/internal/chat/model"
	"miya/internal/interaction"
	"miya/internal/textutil"
)

// setAgentApprovals takes the requests the agents raise. A new one from the
// conversation of an automation run with an access level is answered here
// instead of waiting for a person (docs/action-review.md).
func (c *Controller) setAgentApprovals(v []interaction.Request) {
	if c.Auto == nil || c.Remote != nil || c.Approvals == nil {
		c.SetApprovals(v)
		return
	}
	known := map[string]bool{}
	for _, a := range c.Approvals() {
		known[string(a.ID)] = true
	}
	kept := make([]interaction.Request, 0, len(v))
	for _, a := range v {
		if !known[string(a.ID)] && c.answerUnattended(a) {
			continue
		}
		kept = append(kept, a)
	}
	c.SetApprovals(kept)
	// A request sent for review made a card while kept was built.
	c.syncReviewApprovals()
}

// answerUnattended answers a, when its automation run decides it or, in an
// ordinary conversation, a grant for its project allows it, and reports
// whether it did.
func (c *Controller) answerUnattended(a interaction.Request) bool {
	if a.Kind() != interaction.Approval {
		return false
	}
	t, ok := c.approvalThread(a)
	if !ok {
		return false
	}
	if t.Automation == "" {
		return c.projectGrantAllows(a, t) && c.respondUnattended(a, automation.Answer{Decided: true, Allow: true})
	}
	ask := automation.Ask{Command: textutil.FirstNonEmpty(a.Command, a.GrantRoot, a.Reason, a.Method)}
	if claudeApproval(a) {
		ask = automation.Ask{Tool: a.Command, Input: a.Payload}
	}
	answer := c.Auto.Unattended(t.ID, ask)
	return answer.Decided && c.respondUnattended(a, answer)
}

// respondUnattended sends answer to the agent that asked a.
func (c *Controller) respondUnattended(a interaction.Request, answer automation.Answer) bool {
	responder, ok := c.ApprovalOwner(a).(agent.LegacyResponder)
	if !ok {
		return false
	}
	action := interaction.Decline
	if answer.Allow {
		action = interaction.Allow
	}
	if claudeApproval(a) {
		decision := "decline"
		if answer.Allow {
			decision = "accept"
		}
		responder.RespondNative(a, map[string]string{"decision": decision, "message": answer.Message})
	} else {
		// Codex's reply carries no reason; the run's record keeps it.
		responder.RespondNative(a, codex.EncodeResponse(a, interaction.Response{Action: action}))
	}
	return true
}

// projectGrantAllows reports whether a grant for the project of t, an
// ordinary conversation, allows the command a asks to run.
func (c *Controller) projectGrantAllows(a interaction.Request, t model.Thread) bool {
	command := a.Command
	if claudeApproval(a) {
		if a.Command != "Bash" {
			return false
		}
		var in struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(a.Payload, &in)
		command = in.Command
	}
	return c.Auto.ProjectAllows(c.ThreadWorkspace(t.ID), command)
}
