package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testUnreadHub(t *testing.T) *pushHub {
	t.Helper()
	r := pushRegistration{ID: strings.Repeat("a", 32), Token: strings.Repeat("b", 64), Updated: time.Now()}
	return &pushHub{storageDir: t.TempDir(), devices: map[string]pushRegistration{r.ID: r}, queue: make(chan pushDelivery, 20)}
}
func TestUnreadBadgeCountsConversationsAndRejectsStaleReads(t *testing.T) {
	h := testUnreadHub(t)
	device := strings.Repeat("a", 32)
	first := taskNotice{ID: "done-1", Thread: "1", ServerID: "s1", Agent: agentCodex, Project: "/a", Title: "Done"}
	h.publish(first)
	delivery := <-h.queue
	state := h.notificationState(device)
	if len(state.Unread) != 1 {
		t.Fatal("missing unread conversation")
	}
	oldVersion := state.Unread[0].Version
	h.publish(first)
	if len(h.queue) != 0 || h.notificationState(device).Revision != state.Revision {
		t.Fatal("duplicate notice incremented unread count")
	}
	first.ID = "approval-2"
	h.publish(first)
	newDelivery := <-h.queue
	state = h.markRead(device, first.unreadKey(), oldVersion)
	if len(state.Unread) != 1 {
		t.Fatal("a stale read erased a newer notification")
	}
	second := taskNotice{ID: "done-3", Thread: "2", ServerID: "s2", Agent: agentCodex, Project: "/a", Title: "Done"}
	h.publish(second)
	<-h.queue
	if n, ok := h.notificationFor(newDelivery); !ok || n.Payload.Badge == nil || *n.Payload.Badge != 2 {
		t.Fatal("badge is not an absolute unread conversation count")
	}
	state = h.notificationState(device)
	for _, u := range state.Unread {
		h.markRead(device, u.Key, u.Version)
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
	n := taskNotice{ID: "done", Thread: "1", ServerID: "stable", Agent: agentCodex, Project: "/a"}
	h.publish(n)
	data, err := os.ReadFile(filepath.Join(h.storageDir, "push-unread.json"))
	if err != nil {
		t.Fatal(err)
	}
	restored := &pushHub{devices: h.devices, queue: make(chan pushDelivery, 4)}
	if err := json.Unmarshal(data, &restored.unread); err != nil {
		t.Fatal(err)
	}
	s := restored.notificationState(device)
	if len(s.Unread) != 1 || !unreadMatches(s.Unread[0], Thread{ID: 77, ServerID: "stable"}) {
		t.Fatal("restart lost stable unread identity")
	}
	if unreadMatches(s.Unread[0], Thread{ID: 1, ServerID: "other"}) {
		t.Fatal("reused numeric ID inherited unread state")
	}
	restored.markRead(device, n.unreadKey(), s.Unread[0].Version)
	if len(restored.notificationState(device).Unread) != 0 {
		t.Fatal("read did not clear the badge")
	}
}
func TestMobileUnreadStateIgnoresOldAcknowledgements(t *testing.T) {
	m := testMobileApp()
	m.applyNotifications(phoneNotificationState{Revision: 5, Unread: []phoneUnread{{Key: "a", ServerID: "s1", Agent: agentCodex, Version: 5}}})
	m.applyNotifications(phoneNotificationState{Revision: 4, Unread: []phoneUnread{}})
	if m.desiredBadge() != 1 {
		t.Fatal("old acknowledgement erased newer unread state")
	}
	m.applyNotifications(phoneNotificationState{Revision: 6, Unread: []phoneUnread{}})
	if m.desiredBadge() != 0 {
		t.Fatal("acknowledged read did not clear badge")
	}
	m.pushDisabled = true
	m.applyNotifications(phoneNotificationState{Revision: 7, Unread: []phoneUnread{{Key: "b", Version: 7}}})
	if m.desiredBadge() != 0 {
		t.Fatal("disabled notifications kept the icon badge")
	}
}

func TestMarkAllReadPreservesLaterNotifications(t *testing.T) {
	h := testUnreadHub(t)
	device := strings.Repeat("a", 32)
	one := taskNotice{ID: "one", Thread: "1", ServerID: "s1", Agent: agentCodex}
	h.publish(one)
	version := h.notificationState(device).Revision
	h.publish(taskNotice{ID: "two", Thread: "2", ServerID: "s2", Agent: agentCodex})
	s := h.markAllRead(device, version)
	if len(s.Unread) != 1 || s.Unread[0].ServerID != "s2" {
		t.Fatalf("mark all read erased a later notification: %+v", s)
	}
}
