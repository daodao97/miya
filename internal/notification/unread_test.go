package notification

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"miya/internal/chat/model"
)

func testUnreadHub(t *testing.T) *Hub {
	t.Helper()
	r := Registration{ID: strings.Repeat("a", 32), Token: strings.Repeat("b", 64), Updated: time.Now()}
	return NewQueueHub(t.TempDir(), r)
}

func TestUnreadBadgeCountsConversationsAndRejectsStaleReads(t *testing.T) {
	h := testUnreadHub(t)
	device := strings.Repeat("a", 32)
	first := Notice{ID: "done-1", Thread: "1", ServerID: "s1", Agent: model.AgentCodex, Project: "/a", Title: "Done"}
	h.Publish(first)
	delivery := <-h.queue
	state := h.State(device)
	if len(state.Unread) != 1 {
		t.Fatal("missing unread conversation")
	}
	oldVersion := state.Unread[0].Version
	h.Publish(first)
	if len(h.queue) != 0 || h.State(device).Revision != state.Revision {
		t.Fatal("duplicate notice incremented unread count")
	}
	first.ID = "approval-2"
	h.Publish(first)
	newDelivery := <-h.queue
	state = h.MarkRead(device, first.UnreadKey(), oldVersion)
	if len(state.Unread) != 1 {
		t.Fatal("a stale read erased a newer notification")
	}
	second := Notice{ID: "done-3", Thread: "2", ServerID: "s2", Agent: model.AgentCodex, Project: "/a", Title: "Done"}
	h.Publish(second)
	<-h.queue
	if n, ok := h.notificationFor(newDelivery); !ok || n.Payload.Badge == nil || *n.Payload.Badge != 2 {
		t.Fatal("badge is not an absolute unread conversation count")
	}
	state = h.State(device)
	for _, u := range state.Unread {
		h.MarkRead(device, u.Key, u.Version)
	}
	if n, ok := h.notificationFor(delivery); !ok || *n.Payload.Badge != 0 || n.Payload.Title != "" {
		t.Fatal("a delayed alert restored a read badge")
	}
	n, _ := h.notificationFor(newDelivery)
	data, err := json.Marshal(n.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		APS map[string]any `json:"aps"`
	}
	_ = json.Unmarshal(data, &payload)
	if payload.APS["badge"] != float64(0) || payload.APS["alert"] != nil || payload.APS["sound"] != nil {
		t.Fatalf("badge clearing created a notification: %s", data)
	}
}

func TestUnreadStatePersistsAndResolvesStableIdentity(t *testing.T) {
	h := testUnreadHub(t)
	device := strings.Repeat("a", 32)
	n := Notice{ID: "done", Thread: "1", ServerID: "stable", Agent: model.AgentCodex, Project: "/a"}
	h.Publish(n)
	data, err := os.ReadFile(filepath.Join(h.storageDir, "push-unread.json"))
	if err != nil {
		t.Fatal(err)
	}
	restored := &Hub{devices: h.devices, queue: make(chan pushDelivery, 4)}
	if err := json.Unmarshal(data, &restored.unread); err != nil {
		t.Fatal(err)
	}
	s := restored.State(device)
	if len(s.Unread) != 1 || !s.Unread[0].Matches(model.Thread{ID: 77, ServerID: "stable"}) {
		t.Fatal("restart lost stable unread identity")
	}
	if s.Unread[0].Matches(model.Thread{ID: 1, ServerID: "other"}) {
		t.Fatal("reused numeric ID inherited unread state")
	}
	restored.MarkRead(device, n.UnreadKey(), s.Unread[0].Version)
	if len(restored.State(device).Unread) != 0 {
		t.Fatal("read did not clear the badge")
	}
}

func TestMarkAllReadPreservesLaterNotifications(t *testing.T) {
	h := testUnreadHub(t)
	device := strings.Repeat("a", 32)
	one := Notice{ID: "one", Thread: "1", ServerID: "s1", Agent: model.AgentCodex}
	h.Publish(one)
	version := h.State(device).Revision
	h.Publish(Notice{ID: "two", Thread: "2", ServerID: "s2", Agent: model.AgentCodex})
	s := h.MarkAllRead(device, version)
	if len(s.Unread) != 1 || s.Unread[0].ServerID != "s2" {
		t.Fatalf("mark all read erased a later notification: %+v", s)
	}
}

func TestNotificationEchoesRegisteredDesktop(t *testing.T) {
	desktop := strings.Repeat("c", 24)
	h := NewQueueHub(t.TempDir())
	if err := h.Register(Registration{ID: strings.Repeat("a", 32), Token: strings.Repeat("b", 64), Desktop: desktop}); err != nil {
		t.Fatal(err)
	}
	h.Publish(Notice{ID: "done-1", Thread: "1", ServerID: "s1", Agent: model.AgentCodex, Project: "/a", Title: "Done"})
	n, ok := h.NextDelivery()
	if !ok || n.Payload.Data["desktop"] != desktop {
		t.Fatal("notification does not identify the desktop that sent it")
	}
	if err := h.Register(Registration{ID: strings.Repeat("a", 32), Token: strings.Repeat("b", 64), Desktop: "../x"}); err == nil {
		t.Fatal("accepted an invalid desktop ID")
	}
}
