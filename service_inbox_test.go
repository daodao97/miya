package main

import (
	appmodel "miya/internal/appmodel"
	testing "testing"
)

func TestServiceInboxSchedulesOnceAndKeepsLatestState(t *testing.T) {
	var b appmodel.ServiceInbox
	st := func(status string) appmodel.SvcMessage {
		return appmodel.SvcMessage{State: &appmodel.SvcState{Status: status}}
	}
	msgs := func(thread int) appmodel.SvcMessage {
		return appmodel.SvcMessage{Messages: &appmodel.SvcMessages{Thread: thread}}
	}

	if !b.Push(st("a")) {
		t.Fatal("first push must schedule a drain")
	}
	for _, m := range []appmodel.SvcMessage{msgs(1), st("b"), msgs(2), st("c")} {
		if b.Push(m) {
			t.Fatal("pushes before a take must not schedule again")
		}
	}
	got := b.Take()
	if len(got) != 3 {
		t.Fatalf("take returned %d events, want 3 (two messages, latest state)", len(got))
	}
	if got[0].Messages == nil || got[0].Messages.Thread != 1 || got[1].Messages == nil || got[1].Messages.Thread != 2 {
		t.Fatalf("messages out of order: %+v", got)
	}
	if got[2].State == nil || got[2].State.Status != "c" {
		t.Fatalf("want only the latest state last, got %+v", got[2])
	}
	if !b.Push(st("d")) {
		t.Fatal("a push after take must schedule again")
	}
}
