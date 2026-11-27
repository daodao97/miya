package controller

import (
	"encoding/json"
	"testing"

	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	dg "miya/internal/delegation"
	svcproto "miya/internal/service/protocol"
)

func TestDelegationTimelineProjectsLiveTaskAndFixedReceipt(t *testing.T) {
	const source = "0123456789abcdef0123456789abcdef"
	from := (model.ThreadRef{Source: source, Backend: "codex", SessionID: "parent"}).URL()
	target := (model.ThreadRef{Source: source, Backend: model.AgentClaude, SessionID: "child"}).URL()
	manager, err := dg.Open(dg.NewStore(t.TempDir()), dg.Options{})
	if err != nil {
		t.Fatal(err)
	}
	task, err := manager.Create(dg.StartInput{From: from, RequestID: "review", Agent: model.AgentClaude,
		Workspace: t.TempDir(), Title: "Review\nREADME", Prompt: "Review the README"})
	if err != nil {
		t.Fatal(err)
	}
	call := model.Message{ID: 1, Role: "tool", Kind: "mcpToolCall", Status: "completed", Item: &model.Activity{
		Server: "miya", Tool: "start_task", Arguments: json.RawMessage(`{"request_id":"review","task":"Review the README","agent":"claude"}`),
	}}
	thread := model.Thread{Messages: []model.Message{call}}
	if err := manager.Observe(dg.Event{TaskID: task.ID, Target: target, State: dg.Running, Progress: "Reading README"}); err != nil {
		t.Fatal(err)
	}
	running := delegationTimeline(thread, from, manager).Messages[0]
	if len(running.Delegations) != 1 || running.Delegations[0].State != "running" || running.Delegations[0].Thread != target {
		t.Fatalf("unexpected running call: %+v", running.Delegations)
	}

	if err := manager.Observe(dg.Event{TaskID: task.ID, State: dg.Completed, Result: dg.Result{Text: "All links resolve."}}); err != nil {
		t.Fatal(err)
	}
	raw := manager.NotificationText(dg.Delivery{TaskIDs: []string{task.ID}})
	thread.Messages = append(thread.Messages, model.Message{ID: 2, Role: "user", Content: raw})
	messages := delegationTimeline(thread, from, manager).Messages
	if svcproto.MessageFingerprint(running) == svcproto.MessageFingerprint(messages[0]) {
		t.Fatal("completed child must update the same call over the wire")
	}
	if len(messages[1].Delegations) != 1 || messages[1].Delegations[0].Quote != "All links resolve." || messages[1].Content != raw || messages[1].Role != "user" {
		t.Fatalf("unexpected receipt: %+v", messages[1])
	}
	if messages[0].Delegations[0].TaskID != task.ID || messages[1].Delegations[0].TaskID != task.ID || messages[1].Delegations[0].Read {
		t.Fatalf("cards must name their task: %+v %+v", messages[0].Delegations, messages[1].Delegations)
	}

	read := model.Message{ID: 3, Role: "tool", Kind: "mcpToolCall", Status: "completed", Item: &model.Activity{
		Server: "miya", Tool: "read_task", Arguments: json.RawMessage(`{"task_id":"` + task.ID + `"}`),
	}}
	thread.Messages = append(thread.Messages, read, read)
	thread.Messages[3].ID = 4
	messages = delegationTimeline(thread, from, manager).Messages
	if !messages[1].Delegations[0].Read || !msgfmt.FoldedDelegationRead(messages[2]) || msgfmt.FoldedDelegationRead(messages[3]) {
		t.Fatalf("only the first read after the receipt folds into it: %+v", messages)
	}
	if groups := msgfmt.GroupMessages(messages); len(groups) != 3 || len(groups[2].Messages) != 1 || groups[2].Messages[0].ID != 4 {
		t.Fatalf("folded read stayed in the timeline: %+v", groups)
	}
	if len(thread.Messages[0].Delegations) != 0 || len(thread.Messages[1].Delegations) != 0 || len(thread.Messages[2].Delegations) != 0 {
		t.Fatal("projection mutated the native transcript")
	}

	unrelated := (model.ThreadRef{Source: source, Backend: "codex", SessionID: "other"}).URL()
	messages = delegationTimeline(thread, unrelated, manager).Messages
	if len(messages[0].Delegations) != 0 || len(messages[1].Delegations) != 1 || messages[1].Delegations[0].Quote != "" {
		t.Fatal("timeline looked up a task outside the viewer's conversation")
	}
}
