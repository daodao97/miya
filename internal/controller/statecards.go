package controller

import (
	"time"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/statecard"
)

// StartStateCards loads the state cards this process keeps: the background
// service's, or those of a window that runs without one.
func (c *Controller) StartStateCards(dir string) {
	// A directory that cannot be read leaves no cards; a write says why.
	c.cards, _ = statecard.Open(dir)
}

func stateCardKey(t model.Thread) string {
	return statecard.Key(model.ThreadAgent(t), t.ServerID)
}

// StateCard is the card of t's conversation, when it has one. A card the
// person wrote after is outdated, also when nobody watched them write.
func (c *Controller) StateCard(t model.Thread) (statecard.Card, bool) {
	card, ok := c.cards.Get(stateCardKey(t))
	if ok && !card.Stale && wentOn(t, card) {
		_, err := c.cards.SetStale(card.Key(), true)
		c.stateCardFailed(err)
		card.Stale = true
	}
	return card, ok
}

// WriteStateCard saves card as the card of t's conversation, in place of an
// earlier one. call is the tool call of t's agent that wrote it.
func (c *Controller) WriteStateCard(t model.Thread, call model.Message, card statecard.Card) (statecard.Card, error) {
	card.Backend, card.Session = model.ThreadAgent(t), t.ServerID
	card.Author, card.Stale = statecard.AuthorAgent, false
	card.WrittenAtMs = time.Now().UnixMilli()
	card.Turn, card.Item = call.TurnID, call.ItemID
	if card.Turn == "" {
		card.Turn = t.TurnID
	}
	if err := c.cards.Put(card); err != nil {
		return statecard.Card{}, err
	}
	return card, nil
}

// WaitingThreads lists the conversations that wait for the person. An open
// approval or question of an agent is one, also while its turn runs. So is a
// conversation the person started whose turn ended on a card that names what
// it waits for, until the conversation goes on. Whether the person read the
// conversation changes nothing. A window of the service has the service's
// list.
func (c *Controller) WaitingThreads() appmodel.Waiting {
	if c.Mirror != nil {
		return c.waiting
	}
	var waiting appmodel.Waiting
	add := func(t model.Thread, what string) {
		if waiting == nil {
			waiting = appmodel.Waiting{}
		}
		waiting[appmodel.DesktopUnreadKey(t)] = what
	}
	if c.Approvals != nil {
		for _, a := range c.Approvals() {
			// A review does not hold up the run that asked for it.
			if _, review := reviewOfApproval(a); review {
				continue
			}
			if t, ok := c.approvalThread(a); ok && !t.Loading {
				add(t, "")
			}
		}
	}
	if c.cards.Len() == 0 || c.Threads == nil {
		return waiting
	}
	cards := func(threads []model.Thread) {
		for _, t := range threads {
			// A turn that runs is listed as running, and an automation's
			// conversation has no card.
			if t.Busy || t.Loading || t.Automation != "" || t.ServerID == "" {
				continue
			}
			if card, ok := c.StateCard(t); ok && card.Waiting() {
				add(t, card.WaitingFor)
			}
		}
	}
	cards(c.Threads())
	for path, threads := range c.ProjectCache {
		if path != c.Cwd {
			cards(threads)
		}
	}
	return waiting
}

// stateCardThreadUpdated keeps a conversation's card with it: the card
// follows the conversation to another session, and it is outdated once the
// conversation goes on. It runs for every streamed change, so a process
// without cards leaves at once.
func (c *Controller) stateCardThreadUpdated(old, t model.Thread) {
	if c.cards.Len() == 0 {
		return
	}
	if old.ServerID != "" && t.ServerID != "" && old.ServerID != t.ServerID {
		c.stateCardFailed(c.cards.Move(stateCardKey(old), model.ThreadAgent(t), t.ServerID))
	}
	card, ok := c.cards.Get(stateCardKey(t))
	if !ok || card.Stale || !wentOn(t, card) {
		return
	}
	_, err := c.cards.SetStale(card.Key(), true)
	c.stateCardFailed(err)
}

// stateCardThreadDeleted drops the card of a conversation that is deleted.
func (c *Controller) stateCardThreadDeleted(t model.Thread) {
	c.stateCardFailed(c.cards.Delete(stateCardKey(t)))
}

func (c *Controller) stateCardFailed(err error) {
	if err != nil && c.SetStatus != nil {
		c.SetStatus(err.Error())
	}
}

// wentOn reports whether the conversation went on after its card was
// written: it runs another turn than the one that wrote the card, where the
// agent names its turns, or the person wrote after the card.
func wentOn(t model.Thread, card statecard.Card) bool {
	if card.Turn != "" && t.TurnID != "" && t.TurnID != card.Turn {
		return true
	}
	// Only what follows the call that wrote the card is read.
	for i := len(t.Messages) - 1; i >= 0; i-- {
		m := t.Messages[i]
		if m.Role == "user" && m.SentAtMs > card.WrittenAtMs {
			return true
		}
		if card.Item != "" && m.ItemID == card.Item {
			return false
		}
		if at := max(m.SentAtMs, m.StartedAtMs, m.CompletedAtMs); at != 0 && at <= card.WrittenAtMs {
			return false
		}
	}
	return false
}
