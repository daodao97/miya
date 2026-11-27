package controller

import (
	"slices"
	"strings"
	"time"

	"miya/internal/chat/model"
	"miya/internal/memory"
	"miya/internal/memory/extractor"
	"miya/internal/statecard"
	"miya/internal/textutil"
)

func (c *Controller) stateCardEngine() *extractor.Engine {
	engine, _ := c.Memories().Engine.(*extractor.Engine)
	return engine
}

func eligibleForStateCard(t model.Thread) bool {
	return t.ID != 0 && t.ServerID != "" && !t.Busy && !t.Loading && !t.ReadOnly &&
		!t.MemoryDisabled && !t.StopRequested && !t.StoppedByUser() &&
		t.Automation == "" && t.ExecutionID == "" && len(t.Queue) == 0
}

func (c *Controller) scheduleStateCard(old, t model.Thread) {
	engine := c.stateCardEngine()
	if engine == nil || c.cards == nil {
		return
	}
	if !eligibleForStateCard(t) {
		if !old.Busy || old.ServerID != t.ServerID {
			engine.CancelStateCard(t.ID)
		}
		return
	}
	if !old.Busy && (old.HistoryLoaded || !t.HistoryLoaded) {
		return
	}
	previous, exists := c.cards.Get(stateCardKey(t))
	if exists && !previous.Stale && !wentOn(t, previous) {
		return
	}
	scope, err := memory.ProjectScope(c.ThreadWorkspace(t.ID))
	if err != nil {
		return
	}
	messages := stateCardMessages(t, previous)
	if len(messages) == 0 {
		return
	}
	job := extractor.StateCardJob{
		ThreadID: t.ID, Scope: scope, Session: c.ThreadRef(t).URL(),
		Messages: messages, Purpose: previous.Purpose, Done: previous.Done,
		Remaining: previous.Remaining, References: previous.References,
	}
	id, version := t.ID, stateCardVersion(t)
	engine.ScheduleStateCard(job, func(card statecard.Card) {
		c.Dispatch(func() { c.saveBackfilledStateCard(id, version, card) })
	})
}

func stateCardMessages(t model.Thread, previous statecard.Card) []extractor.StateCardMessage {
	const maxMessages, maxMessage = 12, 1000
	var out []extractor.StateCardMessage
	hasUser := false
	for i := len(t.Messages) - 1; i >= 0 && len(out) < maxMessages; i-- {
		m := t.Messages[i]
		if previous.Item != "" && m.ItemID == previous.Item && hasUser {
			break
		}
		if m.MemoryControl || m.ExecutionID != "" || (m.Role != "user" && m.Role != "assistant" && m.Role != "error") {
			continue
		}
		body := m.Content
		if m.Role == "user" {
			body, _ = model.SplitReferenceContext(memory.StripContext(body))
		}
		if body = strings.TrimSpace(body); body == "" {
			continue
		}
		out = append(out, extractor.StateCardMessage{Role: m.Role, Text: textutil.ClipRunes(body, maxMessage)})
		hasUser = hasUser || m.Role == "user"
	}
	if !hasUser {
		return nil
	}
	slices.Reverse(out)
	return out
}

type cardVersion struct {
	key, turn                    string
	activity, started, completed int64
	count, lastID                int
}

func stateCardVersion(t model.Thread) cardVersion {
	v := cardVersion{key: stateCardKey(t), turn: t.TurnID, activity: t.LastActivityAt, started: t.StartedAtMs, completed: t.CompletedAtMs, count: len(t.Messages)}
	if v.count > 0 {
		v.lastID = t.Messages[v.count-1].ID
	}
	return v
}

func (c *Controller) saveBackfilledStateCard(id int, version cardVersion, card statecard.Card) {
	t := c.ThreadWithID(id)
	if !eligibleForStateCard(t) || len(t.Messages) == 0 || stateCardVersion(t) != version {
		return
	}
	if current, ok := c.StateCard(t); ok && !current.Stale {
		return
	}
	card.Backend, card.Session = model.ThreadAgent(t), t.ServerID
	card.Author, card.Turn, card.WrittenAtMs = statecard.AuthorBackground, t.TurnID, time.Now().UnixMilli()
	card.Item = t.Messages[len(t.Messages)-1].ItemID
	if err := c.cards.Put(card); err != nil {
		c.stateCardFailed(err)
		return
	}
	if c.Revision != nil {
		c.SetRevision(c.Revision() + 1)
	}
}
