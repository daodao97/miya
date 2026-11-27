package controller

import (
	"slices"
	"strings"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	dg "miya/internal/delegation"
)

// ConversationThread is the desktop subscription's presentation snapshot.
func (c *Controller) ConversationThread(id int) model.Thread {
	t := c.ThreadWithID(id)
	return delegationTimeline(t, c.ThreadRef(t).URL(), c.delegationManager())
}

func (c *Controller) delegationManager() *dg.Manager {
	if c.Collaboration == nil {
		return nil
	}
	return c.Collaboration.Manager
}

// Enrich a copy for the viewers. The model still receives the original prompt
// and native history continues to carry it as the start of its own turn.
//
// The call card carries the task's live state and full result; a receipt
// quotes only its opening, and the read_task that follows a receipt is folded
// into it.
func delegationTimeline(t model.Thread, from string, manager *dg.Manager) model.Thread {
	if manager == nil || from == "" {
		return t
	}
	messages, cloned := t.Messages, false
	set := func(i int, cards []model.DelegationCard) {
		if !cloned {
			messages, cloned = slices.Clone(messages), true
		}
		messages[i].Delegations = cards
	}
	type slot struct{ message, card int }
	unread := map[string]slot{}
	for i, m := range t.Messages {
		if requestID, card, ok := msgfmt.DelegationCall(m); ok {
			if requestID != "" && card.State != "failed" && card.State != "interrupted" {
				if task, found := manager.FindRequest(from, requestID); found {
					set(i, []model.DelegationCard{taskCard(card, task, 1200, 14)})
				}
			}
			continue
		}
		if id, ok := msgfmt.DelegationRead(m); ok {
			if at, found := unread[id]; found {
				delete(unread, id)
				messages[at.message].Delegations[at.card].Read = true
				set(i, []model.DelegationCard{{TaskID: id, Read: true}})
			}
			continue
		}
		receipts := msgfmt.DelegationReceipts(m)
		if len(receipts) == 0 {
			continue
		}
		for k, card := range receipts {
			if manager.CanAccess(from, card.TaskID) {
				if task, err := manager.Read(card.TaskID); err == nil {
					receipts[k] = taskCard(card, task, 280, 3)
				}
			}
			unread[card.TaskID] = slot{i, k}
		}
		set(i, receipts)
	}
	t.Messages = messages
	return t
}

func taskCard(card model.DelegationCard, task dg.Task, quoteLimit, quoteLines int) model.DelegationCard {
	card.TaskID, card.Title = task.ID, task.Title
	if card.Agent == "" && task.Agent != "" {
		card.Agent = model.AgentName(task.Agent)
	}
	card.State, card.Thread = string(task.State), task.Target
	card.Quote = delegationExcerpt(task.Result.Text, quoteLimit, quoteLines)
	detail := task.Result.Error
	if !dg.Terminal(task.State) {
		detail = task.Progress
	}
	card.Detail = delegationExcerpt(detail, 320, 4)
	card.Blocked = delegationExcerpt(strings.Join(task.Refused, "\n"), 320, 4)
	card.Expired = task.Result.Availability == dg.ResultExpired
	return card
}

// Bound both the wire preview and the number of short lines it can add to the timeline.
func delegationExcerpt(text string, runes, lines int) string {
	text = strings.TrimSpace(text)
	for i, r := range text {
		if runes == 0 || lines == 0 {
			return strings.TrimSpace(text[:i]) + "…"
		}
		runes--
		if r == '\n' {
			lines--
		}
	}
	return text
}
