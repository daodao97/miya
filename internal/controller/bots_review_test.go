package controller

import (
	"testing"

	"miya/internal/bot"
	"miya/internal/chat/model"
)

// A turn that changes files in the conversation the person talks to a Bot
// in is reviewed, up to maxReviews in a row; a message from the person
// starts the count again.
func TestReviewDue(t *testing.T) {
	c, _ := botTestController(t)
	reviewer := saveTestBot(t, c, bot.Bot{Name: "Kai", Agent: model.AgentCodex, Project: c.Cwd})
	rin := saveTestBot(t, c, bot.Bot{Name: "Rin", Agent: model.AgentClaude, Project: c.Cwd, Reviewer: reviewer.ID})
	c.bots.store.SetSession(rin.ID, "talk")
	rin = storedBot(t, c, rin.ID)
	stats := &model.DiffLineStats{Added: 3, Deleted: 1}
	turn := func(person int, changed bool) model.Thread {
		th := model.Thread{ID: 5, ServerID: "talk", Agent: model.AgentClaude}
		for i := range person {
			th.Messages = append(th.Messages, model.Message{ID: 10*i + 1, Role: "user", Content: "do it", TurnID: "t"})
			th.Messages = append(th.Messages, model.Message{ID: 10*i + 2, Role: "assistant", Content: "done", TurnID: "t"})
		}
		if changed {
			th.Messages = append(th.Messages, model.Message{ID: 99, Role: "assistant", Kind: "diff", Content: "+a", DiffStats: stats, Status: "completed", TurnID: "t"})
		}
		return th
	}

	if _, _, ok := c.reviewDue(rin, turn(1, false)); ok {
		t.Fatal("a turn without changes is reviewed")
	}
	for round := 1; round <= maxReviews; round++ {
		got, n, ok := c.reviewDue(rin, turn(1, true))
		if !ok || got.ID != reviewer.ID || n != round {
			t.Fatalf("review %d: %q, round %d, %v", round, got.Name, n, ok)
		}
	}
	if _, _, ok := c.reviewDue(rin, turn(1, true)); ok {
		t.Fatal("a review past the limit")
	}
	if _, n, ok := c.reviewDue(rin, turn(2, true)); !ok || n != 1 {
		t.Fatalf("after the person wrote again: round %d, %v", n, ok)
	}

	// Neither a task conversation, a Bot without a reviewer, nor a missing
	// reviewer is reviewed.
	task := turn(1, true)
	task.ExecutionID = "task-1"
	if _, _, ok := c.reviewDue(rin, task); ok {
		t.Fatal("a delegated task's conversation is reviewed")
	}
	if _, _, ok := c.reviewDue(storedBot(t, c, reviewer.ID), turn(1, true)); ok {
		t.Fatal("a Bot without a reviewer is reviewed")
	}
	c.bots.store.Delete(reviewer.ID)
	if _, _, ok := c.reviewDue(rin, turn(3, true)); ok {
		t.Fatal("a deleted reviewer reviews")
	}
}
