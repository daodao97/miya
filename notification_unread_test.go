package main

import (
	"miya/internal/notification"
	"testing"
)

func TestMobileUnreadStateIgnoresOldAcknowledgements(t *testing.T) {
	m := testMobileApp()
	m.applyNotifications(notification.PhoneState{Revision: 5, Unread: []notification.Unread{{Key: "a", ServerID: "s1", Agent: agentCodex, Version: 5}}})
	m.applyNotifications(notification.PhoneState{Revision: 4, Unread: []notification.Unread{}})
	if m.desiredBadge() != 1 {
		t.Fatal("old acknowledgement erased newer unread state")
	}
	m.applyNotifications(notification.PhoneState{Revision: 6, Unread: []notification.Unread{}})
	if m.desiredBadge() != 0 {
		t.Fatal("acknowledged read did not clear badge")
	}
	m.pushDisabled = true
	m.applyNotifications(notification.PhoneState{Revision: 7, Unread: []notification.Unread{{Key: "b", Version: 7}}})
	if m.desiredBadge() != 0 {
		t.Fatal("disabled notifications kept the icon badge")
	}
}
