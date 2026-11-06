package notification

import (
	"testing"
	"time"

	"miya/internal/chat/model"
)

func TestCompletionNoticeTransitions(t *testing.T) {
	old := model.Thread{ID: 7, Busy: true}
	next := model.Thread{ID: 7, CompletedAtMs: 42, Title: "完成工作"}
	n, ok := Completion(old, next)
	if !ok || n.Thread != "7" || n.Body != "Task: "+next.Title {
		t.Fatalf("missing completion: %+v", n)
	}
	next.Busy = true
	if _, ok := Completion(old, next); ok {
		t.Fatal("notified while running")
	}
	next.Busy = false
	next.Status = "Stopped"
	if _, ok := Completion(old, next); ok {
		t.Fatal("notified manual stop")
	}
	next.Status = ""
	old.Busy = false
	if _, ok := Completion(old, next); ok {
		t.Fatal("notified restored history")
	}
}
func TestPushRegistrationRejectsInvalidInput(t *testing.T) {
	h := &Hub{devices: map[string]Registration{}}
	for _, r := range []Registration{{}, {ID: "not-a-device"}, {ID: "01234567890123456789012345678901", Token: "invalid"}} {
		if h.Register(r) == nil {
			t.Fatal("accepted malformed registration")
		}
	}
	if len(h.devices) != 0 {
		t.Fatal("invalid registration changed devices")
	}
}
func TestDesktopPresenceSuppressesPhone(t *testing.T) {
	h := &Hub{devices: map[string]Registration{"phone": {}}, queue: make(chan pushDelivery, 1)}
	h.desktopUntil = time.Now().Add(time.Minute)
	h.Publish(Notice{ID: "notice"})
	if len(h.queue) != 0 {
		t.Fatal("phone notified while desktop present")
	}
	h.desktopUntil = time.Time{}
	h.devices["phone"] = Registration{Updated: time.Now()}
	h.Publish(Notice{ID: "notice"})
	if len(h.queue) != 1 {
		t.Fatal("away desktop suppressed phone")
	}
}
