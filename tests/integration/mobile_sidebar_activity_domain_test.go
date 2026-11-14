//go:build !mygo_ui

package integration

import (
	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/notification"
	"testing"
)

func TestMobileSidebarCompletionUnreadCompatibility(t *testing.T) {
	m := testPhoneClient()
	thread := m.State.Projects[0].Threads[0]
	m.State.Notifications.Unread = []notification.Unread{{ServerID: thread.ServerID}}
	_, completed := m.SidebarActivityThreads()
	if len(completed) != 1 {
		t.Fatal("older desktops should still use per-phone unread completions")
	}
	m.State.CompletionUnread = map[string]int64{}
	_, completed = m.SidebarActivityThreads()
	if len(completed) != 0 {
		t.Fatal("stale push unread state must not override an authoritative shared completion state")
	}
	m.State.CompletionUnread[appmodel.DesktopUnreadKey(thread)] = 100
	m.State.Notifications.Unread = nil
	_, completed = m.SidebarActivityThreads()
	if len(completed) != 1 {
		t.Fatal("completion activity should work with push notifications disabled")
	}
}

func mobileActivityFixture() (first, second, done model.Thread) {
	return model.Thread{ID: 1, ServerID: "s1", Title: "Current project running", Busy: true, LastActivityAt: 10},
		model.Thread{ID: 2, ServerID: "s2", Title: "Other project running", Busy: true, LastActivityAt: 20},
		model.Thread{ID: 3, ServerID: "done", Title: "Other project result", CompletedAtMs: 100, LastActivityAt: 30}
}
