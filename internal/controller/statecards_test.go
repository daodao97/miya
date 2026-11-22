package controller

import (
	"errors"
	"path/filepath"
	"testing"

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
