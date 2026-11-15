package controller

import (
	"miya/internal/agent"
	codex "miya/internal/agent/codex/native"
	"miya/internal/automation"
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

// answerUnattended answers a, when its automation run decides it, and
// reports whether it did.
func (c *Controller) answerUnattended(a interaction.Request) bool {
	if a.Kind() != interaction.Approval {
		return false
	}
	t, ok := c.approvalThread(a)
	if !ok || t.Automation == "" {
		return false
	}
	ask := automation.Ask{Command: textutil.FirstNonEmpty(a.Command, a.GrantRoot, a.Reason, a.Method)}
	if claudeApproval(a) {
		ask = automation.Ask{Tool: a.Command, Input: a.Payload}
	}
	answer := c.Auto.Unattended(t.ID, ask)
	if !answer.Decided {
		return false
	}
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
