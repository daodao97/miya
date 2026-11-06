package integration

import (
	"testing"

	"miya/internal/appmodel"
	"miya/internal/notification"
)

func TestMobileUnreadStateIgnoresOldAcknowledgements(t *testing.T) {
	m := testMobileApp()
	m.ApplyNotifications(notification.PhoneState{Revision: 5, Unread: []notification.Unread{{Key: "a", ServerID: "s1", Agent: appmodel.AgentCodex, Version: 5}}})
	m.ApplyNotifications(notification.PhoneState{Revision: 4, Unread: []notification.Unread{}})
	if m.DesiredBadge() != 1 {
		t.Fatal("old acknowledgement erased newer unread state")
	}
	m.ApplyNotifications(notification.PhoneState{Revision: 6, Unread: []notification.Unread{}})
	if m.DesiredBadge() != 0 {
		t.Fatal("acknowledged read did not clear badge")
	}
	m.PushDisabled = true
	m.ApplyNotifications(notification.PhoneState{Revision: 7, Unread: []notification.Unread{{Key: "b", Version: 7}}})
	if m.DesiredBadge() != 0 {
		t.Fatal("disabled notifications kept the icon badge")
	}
}
