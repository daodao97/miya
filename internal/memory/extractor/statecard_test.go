package extractor

import (
	"context"
	"testing"
	"time"

	claude "miya/internal/agent/claude/native"
	"miya/internal/chat/model"
	"miya/internal/memory"
	"miya/internal/statecard"
)

func TestStateCardBackfillHonorsSettingsAndQuota(t *testing.T) {
	scope, err := memory.ProjectScope(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := memory.New(t.TempDir())
	e := &Engine{store: store, ctx: context.Background(), wake: make(chan struct{}, 1)}
	runs, saved := 0, 0
	e.summarize = func(_ context.Context, _ claude.CLI, agent string, _ StateCardJob) (statecard.Card, error) {
		runs++
		if agent != model.AgentCodex {
			t.Fatalf("used %q, want configured Codex", agent)
		}
		return statecard.Card{Purpose: "修复登录"}, nil
	}
	ref := model.ThreadRef{Source: "0123456789abcdef0123456789abcdef", Backend: model.AgentClaude, SessionID: "session"}
	job := StateCardJob{ThreadID: 1, Scope: scope, Session: ref.URL()}
	schedule := func() { e.ScheduleStateCard(job, func(statecard.Card) { saved++ }) }
	schedule()
	if !e.stateCardOnce() || runs != 0 || saved != 0 {
		t.Fatal("ran with automatic extraction disabled")
	}
	if err := store.Configure(scope, memory.Settings{AutoExtract: true, ExtractorAgent: model.AgentCodex}); err != nil {
		t.Fatal(err)
	}
	e.SetStateCardHold(func(agent string) bool { return agent == model.AgentCodex })
	schedule()
	if !e.stateCardOnce() || len(e.cards) != 1 || runs != 0 || saved != 0 {
		t.Fatal("the quota hold did not defer the job")
	}
	e.SetStateCardHold(nil)
	e.cards[0].notBefore = time.Time{}
	if !e.stateCardOnce() || runs != 1 || saved != 1 {
		t.Fatal("deferred job did not run after quota became available")
	}
	schedule()
	if err := store.ConfigureSession(scope, job.Session, true); err != nil {
		t.Fatal(err)
	}
	if !e.stateCardOnce() || runs != 1 || saved != 1 {
		t.Fatal("ran with automatic extraction disabled for the session")
	}
}

func TestStateCardBackfillCannotInventWaitingOrVerification(t *testing.T) {
	for _, raw := range []string{
		`{"purpose":"p","waiting_for":"invented"}`,
		`{"purpose":"p","checked":["invented"]}`,
		`{"purpose":"p"} {}`,
	} {
		if _, err := decodeStateCard([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if card, err := decodeStateCard([]byte(`{"purpose":" p ","done":[],"remaining":["r"],"references":[]}`)); err != nil || card.Purpose != "p" {
		t.Fatalf("valid card = %+v, error = %v", card, err)
	}
}
