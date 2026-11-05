package main

import (
	"strings"
	"testing"
)

// A phone that registers again under a new device ID, with the APNs token it
// had, replaces its old registration and that registration's unread state;
// other phones, and a disabled registration, replace nothing.
func TestPushRegisterReplacesDeviceWithSameToken(t *testing.T) {
	t.Setenv("MIYA_DIR", t.TempDir())
	h := &pushHub{storageDir: t.TempDir(), devices: map[string]pushRegistration{}, queue: make(chan pushDelivery, 4)}
	token := strings.Repeat("ab", 32)
	oldID, newID, otherID := strings.Repeat("1", 32), strings.Repeat("2", 32), strings.Repeat("3", 32)
	if err := h.register(pushRegistration{ID: oldID, Token: token}); err != nil {
		t.Fatal(err)
	}
	if err := h.register(pushRegistration{ID: otherID, Token: strings.Repeat("cd", 32)}); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.unreadDeviceLocked(oldID).Threads["thread"] = phoneUnread{}
	h.mu.Unlock()
	if err := h.register(pushRegistration{ID: newID, Token: token}); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.devices[oldID]; ok {
		t.Fatal("the old registration of the token was kept")
	}
	if _, ok := h.devices[newID]; !ok {
		t.Fatal("the new registration is missing")
	}
	if _, ok := h.devices[otherID]; !ok {
		t.Fatal("another phone's registration was dropped")
	}
	if _, ok := h.unread[oldID]; ok {
		t.Fatal("the old registration's unread state was kept")
	}
	if err := h.register(pushRegistration{ID: oldID, Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.devices[newID]; !ok {
		t.Fatal("a disabled registration dropped the token's device")
	}
	if len(h.devices) != 3 {
		t.Fatalf("devices = %d, want 3", len(h.devices))
	}
}
