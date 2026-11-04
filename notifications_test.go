package main

import (
	"testing"
	"time"
)

func TestCompletionNoticeTransitions(t *testing.T) {
	old := Thread{ID: 7, Busy: true}
	next := Thread{ID: 7, CompletedAtMs: 42, Title: "完成工作"}
	n, ok := completionNotice(old, next)
	if !ok || n.Thread != "7" || n.Body != "Task: "+next.Title {
		t.Fatalf("missing completion: %+v", n)
	}
	next.Busy = true
	if _, ok := completionNotice(old, next); ok {
		t.Fatal("notified while running")
	}
	next.Busy = false
	next.Status = "Stopped"
	if _, ok := completionNotice(old, next); ok {
		t.Fatal("notified manual stop")
	}
	next.Status = ""
	old.Busy = false
	if _, ok := completionNotice(old, next); ok {
		t.Fatal("notified restored history")
	}
}
func TestPushRegistrationRejectsInvalidInput(t *testing.T) {
	h := &pushHub{devices: map[string]pushRegistration{}}
	for _, r := range []pushRegistration{{}, {ID: "not-a-device"}, {ID: "01234567890123456789012345678901", Token: "invalid"}} {
		if h.register(r) == nil {
			t.Fatal("accepted malformed registration")
		}
	}
	if len(h.devices) != 0 {
		t.Fatal("invalid registration changed devices")
	}
}
func TestDesktopPresenceSuppressesPhone(t *testing.T) {
	h := &pushHub{devices: map[string]pushRegistration{"phone": {}}, queue: make(chan pushDelivery, 1)}
	h.desktopUntil = time.Now().Add(time.Minute)
	h.publish(taskNotice{ID: "notice"})
	if len(h.queue) != 0 {
		t.Fatal("phone notified while desktop present")
	}
	h.desktopUntil = time.Time{}
	h.devices["phone"] = pushRegistration{Updated: time.Now()}
	h.publish(taskNotice{ID: "notice"})
	if len(h.queue) != 1 {
		t.Fatal("away desktop suppressed phone")
	}
}
