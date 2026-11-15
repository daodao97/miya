package controller

import (
	"encoding/json"
	"strings"

	"miya/internal/automation"
	"miya/internal/interaction"
	"miya/internal/textutil"
)

// A review an automation sent to the person is also a card among the
// approvals, so it waits where every other approval waits, on the desktop
// and the phone (docs/action-review.md).
const reviewApprovalPrefix = "miya:review:"

// reviewOfApproval is the review a card stands for.
func reviewOfApproval(a interaction.Request) (string, bool) {
	var id string
	if json.Unmarshal(a.ID, &id) != nil || !strings.HasPrefix(id, reviewApprovalPrefix) {
		return "", false
	}
	return strings.TrimPrefix(id, reviewApprovalPrefix), true
}

// syncReviewApprovals keeps one card for each pending review, and none for
// a review that was decided or expired.
func (c *Controller) syncReviewApprovals() {
	if c.Auto == nil || c.Approvals == nil || c.SetApprovals == nil || c.Remote != nil {
		return
	}
	pending := c.Auto.Store.PendingReviews("")
	wanted := map[string]bool{}
	for _, r := range pending {
		wanted[r.ID] = true
	}
	current := c.Approvals()
	next := make([]interaction.Request, 0, len(current)+len(pending))
	have := map[string]bool{}
	changed := false
	for _, a := range current {
		if id, ok := reviewOfApproval(a); ok {
			if !wanted[id] {
				changed = true
				continue
			}
			have[id] = true
		}
		next = append(next, a)
	}
	for _, r := range pending {
		if !have[r.ID] {
			next = append(next, c.reviewApproval(r))
			changed = true
		}
	}
	if changed {
		c.SetApprovals(next)
	}
}

// reviewApproval is the card of a review, in the conversation of the run
// that asked.
func (c *Controller) reviewApproval(r automation.Review) interaction.Request {
	id, _ := json.Marshal(reviewApprovalPrefix + r.ID)
	name := r.Automation
	if i, ok := c.Auto.Store.Find(r.Automation); ok {
		name = c.Auto.Store.Automations[i].Name
	}
	reason := name
	if r.Instructions != "" {
		reason += " · " + textutil.ClipRunes(r.Instructions, 200)
	}
	return interaction.Request{ID: id, Agent: "miya", Method: "miya/review", ThreadID: c.ThreadWithID(r.Thread).ServerID,
		Reason: reason, Command: r.Action, Cwd: r.Dir}
}

// answerReview decides on the review a card stands for: allowing approves
// it once. Approving its kind is on the automation's page.
func (c *Controller) answerReview(id string, result any) {
	data, _ := json.Marshal(result)
	var r struct {
		Decision string `json:"decision"`
	}
	_ = json.Unmarshal(data, &r)
	if c.Auto != nil {
		if err := c.Auto.DecideReview(id, r.Decision == "accept", automation.ScopeOnce, ""); err != nil && c.SetStatus != nil {
			c.SetStatus(err.Error())
		}
	}
	c.syncReviewApprovals()
}
