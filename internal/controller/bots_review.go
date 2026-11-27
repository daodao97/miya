package controller

import (
	"fmt"
	"time"

	"miya/internal/async"
	"miya/internal/bot"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/delegation/coordinator"
)

// A Bot's reviewer (docs/personal-agent.md, P4): each turn that changes files
// in the conversation the person talks to a Bot in goes, read only, to the
// Bot's reviewer. The review comes back to the conversation as a delegated
// task's result, so the Bot fixes what it finds, and the fix is reviewed
// again, up to maxReviews in a row.

// maxReviews bounds the automatic reviews of a conversation in a row: after
// that many without a message from the person, the work waits for them.
const maxReviews = 3

// reviewState is how many automatic reviews a conversation had since the
// person last wrote in it, and how many messages the person had written.
type reviewState struct{ person, rounds int }

// personMessages counts what the person wrote in a conversation, leaving out
// what Miya and other agents sent to it.
func personMessages(t model.Thread) int {
	n := 0
	for _, m := range t.Messages {
		if m.Role == "user" && !m.MemoryControl && m.ExecutionID == "" {
			n++
		}
	}
	return n
}

// reviewDue says whether the turn that just ended in t, a conversation of b,
// is to be reviewed, and by which Bot, and counts the review.
func (c *Controller) reviewDue(b bot.Bot, t model.Thread) (bot.Bot, int, bool) {
	if b.Reviewer == "" || b.Reviewer == b.ID || t.ServerID == "" || t.ExecutionID != "" || t.Automation != "" || !b.Owns(t.ServerID) {
		return bot.Bot{}, 0, false
	}
	i, ok := c.bots.store.Find(b.Reviewer)
	if !ok || c.bots.store.Bots[i].Project != b.Project {
		return bot.Bot{}, 0, false
	}
	groups := msgfmt.ThreadMessageGroups(t, msgfmt.GroupMessages(t.Messages))
	if len(groups) == 0 {
		return bot.Bot{}, 0, false
	}
	if last := groups[len(groups)-1]; last.User || last.DiffStats.Added+last.DiffStats.Deleted == 0 {
		return bot.Bot{}, 0, false
	}
	person := personMessages(t)
	st := c.bots.reviews[t.ID]
	if st.person != person {
		st = reviewState{person: person}
	}
	if st.rounds >= maxReviews {
		return bot.Bot{}, 0, false
	}
	st.rounds++
	if c.bots.reviews == nil {
		c.bots.reviews = map[int]reviewState{}
	}
	c.bots.reviews[t.ID] = st
	return c.bots.store.Bots[i], st.rounds, true
}

// reviewTurn starts the review of a turn that is due one, as a task the
// conversation delegates to the reviewer, read only.
func (c *Controller) reviewTurn(b bot.Bot, t model.Thread) {
	if c.Collaboration == nil {
		return
	}
	reviewer, round, ok := c.reviewDue(b, t)
	if !ok {
		return
	}
	args := coordinator.Args{
		RequestID: fmt.Sprintf("review-%s-%d", t.ServerID, time.Now().UnixNano()),
		Bot:       reviewer.Name,
		Access:    "read",
		Context:   "caller_summary",
		Notify:    "on_finish",
		Title:     fmt.Sprintf("Review of %s's changes", b.Name),
		Task: fmt.Sprintf("Review, read only, the changes %s made to files in its last turn of the conversation referenced below. "+
			"Check them against what was asked and against the project. Report each concrete problem with its file and line and what to change, "+
			"or say plainly that there is none. Do not change any files. This is automatic review %d of at most %d in a row.", b.Name, round, maxReviews),
	}
	id := t.ID
	c.Dispatch(func() {
		reply, err := c.startCollaboration(c.ThreadWithID(id), "start_task", args)
		if err != nil {
			c.reviewFailed(err)
			return
		}
		if run, ok := reply.(async.Reply); ok {
			go func() {
				if _, err := run(); err != nil {
					c.Dispatch(func() { c.reviewFailed(err) })
				}
			}()
		}
	})
}

func (c *Controller) reviewFailed(err error) {
	if c.SetStatus != nil {
		c.SetStatus(tr("Automatic review not started: ") + err.Error())
	}
}
