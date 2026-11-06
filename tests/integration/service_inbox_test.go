package integration

import (
	"testing"

	"miya/internal/svcproto"
)

func TestServiceInboxSchedulesOnceAndKeepsLatestState(t *testing.T) {
	var b svcproto.Inbox
	st := func(status string) svcproto.Envelope {
		return svcproto.Envelope{State: &svcproto.State{Status: status}}
	}
	msgs := func(thread int) svcproto.Envelope {
		return svcproto.Envelope{Messages: &svcproto.ThreadMessages{Thread: thread}}
	}

	if !b.Push(st("a")) {
		t.Fatal("first push must schedule a drain")
	}
	for _, m := range []svcproto.Envelope{msgs(1), st("b"), msgs(2), st("c")} {
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
