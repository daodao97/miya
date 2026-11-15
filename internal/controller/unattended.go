package controller

import (
	"encoding/json"
	"fmt"
	agentpolicy "miya/internal/agent/policy"
	"miya/internal/approval"
	"strings"

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
	if c.Remote != nil || c.Approvals == nil {
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
		if c.projectGrantAllows(a, t) {
			return c.respondUnattended(a, automation.Answer{Decided: true, Allow: true})
		}
		answer := c.delegatedAnswer(a, t)
		return answer.Decided && c.respondUnattended(a, answer)
	}
	if c.Auto == nil {
		return false
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
	return c.Auto != nil && c.Auto.ProjectAllows(c.ThreadWorkspace(t.ID), command)
}

// askOf is a as the approval rules read it.
func askOf(a interaction.Request) approval.Request {
	if claudeApproval(a) {
		return approval.Request{Tool: a.Command, Input: a.Payload}
	}
	return approval.Request{Command: textutil.FirstNonEmpty(a.Command, a.GrantRoot, a.Reason, a.Method)}
}

// delegatedAnswer answers a request from the conversation of a running
// delegated task with an access level: local work its level allows goes on,
// anything else is refused and noted on the task for the delegating
// conversation (docs/delegation-access.md).
func (c *Controller) delegatedAnswer(a interaction.Request, t model.Thread) automation.Answer {
	access := t.PermissionPolicy
	if access != agentpolicy.Read && access != agentpolicy.Edit || c.Collaboration == nil {
		return automation.Answer{}
	}
	task := c.Collaboration.TaskOfThread(t.ID)
	if task == "" {
		return automation.Answer{}
	}
	ask := askOf(a)
	if strings.HasPrefix(ask.Tool, "mcp__miya__") || access == agentpolicy.Read && ask.Tool == "Bash" && approval.ReadOnlyCommand(approval.BashCommand(ask)) {
		return automation.Answer{Decided: true, Allow: true}
	}
	local, what := approval.Classify(c.ThreadWorkspace(t.ID), ask)
	if local && access == agentpolicy.Edit {
		return automation.Answer{Decided: true, Allow: true}
	}
	// For a command that runs more than one thing, name the parts that
	// reach outside: they are what the delegating conversation should do.
	// Clip them, not the note after them, to fit the record.
	command := ask.Command // a Codex request; empty for Claude's tools
	if ask.Tool == "Bash" {
		command = approval.BashCommand(ask)
	}
	if part := approval.OutsidePart(command); part != "" && part != strings.TrimSpace(what) {
		what = textutil.ClipRunes(part, 160) + " (nothing else in that command ran)"
	}
	if c.Collaboration.Manager != nil {
		_ = c.Collaboration.Manager.RecordRefused(task, textutil.ClipRunes(what, 200))
	}
	if local {
		return automation.Answer{Decided: true, Message: fmt.Sprintf("Miya refused %s: this delegated task is read only (access: read). Do not try another way to change anything; say in your final reply what should be changed, and the delegating conversation will take it up.", what)}
	}
	return automation.Answer{Decided: true, Message: fmt.Sprintf("Miya refused %s: this delegated task may not act outside the workspace (access: %s). Do not try another way to do it. Finish the work that does not depend on it, and say in your final reply what the delegating conversation should do; Miya lists this action for it too.", what, access)}
}
