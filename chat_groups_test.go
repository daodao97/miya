package main

import (
	appmodel "miya/internal/appmodel"
	testing "testing"
)

func TestResponseGroupingBoundaries(t *testing.T) {
	messages := []appmodel.Message{
		{ID: 1, Role: "user", Content: "question"},
		{ID: 2, Role: "assistant", TurnID: "a", Content: "first"},
		{ID: 3, Role: "tool", TurnID: "a", Content: "output"},
		{ID: 4, Role: "assistant", TurnID: "a", Content: "final"},
		{ID: 5, Role: "assistant", TurnID: "b", Content: "next turn"},
		{ID: 6, Role: "user", Content: "followup"},
		{ID: 7, Role: "assistant", Content: "reply"},
	}
	groups := appmodel.GroupMessages(messages)
	if len(groups) != 5 || len(groups[1].Messages) != 3 {
		t.Fatalf("incorrect grouping: %+v", groups)
	}
	if got := appmodel.MessageGroupText(groups[1]); got != "first\n\noutput\n\nfinal" {
		t.Fatal(got)
	}
	if messages[1].Content != "first" {
		t.Fatal("source mutated")
	}
}

func TestActivitiesStayWithOneAssistantResponse(t *testing.T) {
	groups := appmodel.GroupMessages([]appmodel.Message{
		{ID: 1, Role: "assistant", TurnID: "turn-a", Content: "answer"},
		{ID: 2, Role: "activity", Kind: "reasoning", TurnID: "turn-b", Content: "thinking"},
		{ID: 3, Role: "tool", Kind: "commandExecution", TurnID: "turn-b", Content: "command"},
		{ID: 4, Role: "assistant", TurnID: "turn-b", Content: "continued"},
	})
	if len(groups) != 1 || len(groups[0].Messages) != 3 {
		t.Fatalf("response was split by activity metadata: %+v", groups)
	}
}

func TestReasoningHiddenWithoutEmptyGroups(t *testing.T) {
	messages := []appmodel.Message{
		{ID: 1, Role: "activity", Kind: "reasoning", Content: "internal summary"},
		{ID: 2, Role: "tool", Kind: "commandExecution"},
		{ID: 3, Role: "activity", Kind: "Reasoning"},
		{ID: 4, Role: "tool", Kind: "commandExecution"},
		{ID: 5, Role: "error", Kind: "error", Content: "capacity"},
	}
	if groups := appmodel.GroupMessages(messages[:1]); len(groups) != 0 {
		t.Fatalf("empty response: %+v", groups)
	}
	groups := appmodel.GroupMessages(messages)
	if len(groups) != 1 || groups[0].ID != 2 || len(groups[0].Messages) != 3 {
		t.Fatalf("unexpected visible groups: %+v", groups)
	}
	runs := appmodel.ActivityRuns(groups[0].Messages)
	if len(runs) != 2 || len(runs[0].Messages) != 2 || !runs[0].Activity || runs[1].Activity {
		t.Fatalf("tool chain/error changed: %+v", runs)
	}
	if messages[0].Content != "internal summary" || len(messages) != 5 {
		t.Fatal("source messages changed")
	}
}

func TestElapsedLabelsMatchTurnState(t *testing.T) {
	messages := []appmodel.Message{{StartedAtMs: 1_000, CompletedAtMs: 12_500}}
	if got := appmodel.TurnElapsedLabel(messages, false); got != "Worked for 11s" {
		t.Fatalf("completed elapsed label = %q", got)
	}
	if got := appmodel.FormatElapsed(900); got != "under 1s" {
		t.Fatalf("sub-second elapsed label = %q", got)
	}
}
