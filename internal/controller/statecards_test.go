package controller

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"miya/internal/chat/model"
	"miya/internal/statecard"
)

// A controller that started no cards, as a window of the service, finds
// none, says where they are kept when asked to write one, and does nothing
// when a conversation changes or is deleted.
func TestStateCardsAreKeptOnlyWhereTheyWereStarted(t *testing.T) {
	c := &Controller{}
	th := model.Thread{ID: 1, Agent: model.AgentCodex, ServerID: "session-a"}
	if _, err := c.WriteStateCard(th, model.Message{ItemID: "item-1"}, statecard.Card{Purpose: "迁移配置"}); !errors.Is(err, statecard.ErrNoStore) {
		t.Fatalf("write without a store: %v", err)
	}
	if card, ok := c.StateCard(th); ok {
		t.Fatalf("a card without a store: %+v", card)
	}
	c.stateCardThreadUpdated(th, th)
	c.stateCardThreadDeleted(th)
}

func TestBackfilledStateCardLosesToNewActivityAndAgentUpdates(t *testing.T) {
	c := &Controller{}
	c.StartStateCards(t.TempDir())
	thread := model.Thread{
		ID: 7, Agent: model.AgentClaude, ServerID: "session-a",
		Messages: []model.Message{
			{ID: 1, Role: "user", Content: "修复登录"},
			{ID: 2, Role: "assistant", Content: "还需要验证迁移", ItemID: "answer"},
		},
	}
	c.Threads = func() []model.Thread { return []model.Thread{thread} }
	version := stateCardVersion(thread)
	card := statecard.Card{Purpose: "修复登录", Remaining: []string{"验证迁移"}}
	thread.Busy = true
	c.saveBackfilledStateCard(thread.ID, version, card)
	thread.Busy = false
	thread.LastActivityAt++
	c.saveBackfilledStateCard(thread.ID, version, card)
	if _, ok := c.StateCard(thread); ok {
		t.Fatal("saved a card for a superseded conversation snapshot")
	}

	version = stateCardVersion(thread)
	c.saveBackfilledStateCard(thread.ID, version, card)
	got, ok := c.StateCard(thread)
	if !ok || got.Author != statecard.AuthorBackground || got.Item != "answer" || got.Waiting() {
		t.Fatalf("background card = %+v, found %v", got, ok)
	}
	_, err := c.WriteStateCard(thread, model.Message{ItemID: "update"}, statecard.Card{Purpose: "校验登录迁移"})
	if err != nil {
		t.Fatal(err)
	}
	c.saveBackfilledStateCard(thread.ID, version, card)
	got, _ = c.StateCard(thread)
	if got.Author != statecard.AuthorAgent || got.Purpose != "校验登录迁移" {
		t.Fatalf("late background result replaced the agent's card: %+v", got)
	}
	thread.Messages = append(thread.Messages, model.Message{ID: 3, Role: "user", SentAtMs: time.Now().Add(time.Second).UnixMilli()})
	if stale, _ := c.StateCard(thread); !stale.Stale {
		t.Fatal("new user message did not mark the card stale")
	}
}

func TestStateCardIsDroppedWithItsConversation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "status")
	c := &Controller{}
	c.StartStateCards(dir)
	gone := model.Thread{ID: 1, Agent: model.AgentCodex, ServerID: "session-a"}
	kept := model.Thread{ID: 2, Agent: model.AgentCodex, ServerID: "session-b"}
	for _, th := range []model.Thread{gone, kept} {
		if _, err := c.WriteStateCard(th, model.Message{ItemID: "item-1"}, statecard.Card{Purpose: "迁移配置"}); err != nil {
			t.Fatal(err)
		}
	}
	c.stateCardThreadDeleted(gone)
	// A conversation that never had a session has no card to drop.
	c.stateCardThreadDeleted(model.Thread{ID: 3, Agent: model.AgentCodex})
	store, err := statecard.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get(stateCardKey(gone)); ok || store.Len() != 1 {
		t.Fatalf("the deleted conversation's card stayed: %d cards", store.Len())
	}
	if card, ok := c.StateCard(kept); !ok || card.Session != "session-b" {
		t.Fatalf("another conversation lost its card: %+v %v", card, ok)
	}
}
