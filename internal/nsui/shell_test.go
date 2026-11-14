package nsui

import (
	"testing"
	"time"

	"miya/internal/chat/model"
)

func TestThreadGroups(t *testing.T) {
	now := time.Date(2026, 11, 14, 15, 0, 0, 0, time.Local)
	at := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	threads := []model.Thread{
		{ID: 1, LastActivityAt: at(2 * time.Hour)},
		{ID: 2, LastActivityAt: at(26 * time.Hour)},
		{ID: 3, Busy: true, LastActivityAt: at(400 * time.Hour)},
		{ID: 4, LastActivityAt: at(30 * time.Minute)},
		{ID: 5, LastActivityAt: at(90 * 24 * time.Hour)},
	}
	got := map[string][]int{}
	var order []string
	for _, g := range threadGroups(threads, now) {
		order = append(order, g.key)
		for _, th := range g.threads {
			got[g.key] = append(got[g.key], th.ID)
		}
	}
	wantOrder := []string{"running", "today", "yesterday", "older"}
	if len(order) != len(wantOrder) {
		t.Fatalf("groups = %v, want %v", order, wantOrder)
	}
	for i := range wantOrder {
		if order[i] != wantOrder[i] {
			t.Fatalf("groups = %v, want %v", order, wantOrder)
		}
	}
	if g := got["today"]; len(g) != 2 || g[0] != 4 || g[1] != 1 {
		t.Errorf("today = %v, want most recent first [4 1]", g)
	}
	if g := got["running"]; len(g) != 1 || g[0] != 3 {
		t.Errorf("running = %v", g)
	}
}

func TestStepThreadFollowsSidebarOrder(t *testing.T) {
	now := time.Date(2026, 11, 14, 15, 0, 0, 0, time.Local)
	at := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	// Sidebar order: 3 (running), 4, 1 (today), 2 (yesterday).
	threads := []model.Thread{
		{ID: 1, LastActivityAt: at(2 * time.Hour)},
		{ID: 2, LastActivityAt: at(26 * time.Hour)},
		{ID: 3, Busy: true},
		{ID: 4, LastActivityAt: at(30 * time.Minute)},
	}
	for _, tc := range []struct{ current, step, want int }{
		{3, 1, 4}, {4, 1, 1}, {2, 1, 3}, {3, -1, 2}, {99, 1, 3}, {99, -1, 2},
	} {
		if got := stepThread(threads, tc.current, tc.step, now); got != tc.want {
			t.Errorf("step(%d, %d) = %d, want %d", tc.current, tc.step, got, tc.want)
		}
	}
	if got := stepThread(nil, 5, 1, now); got != 5 {
		t.Errorf("no threads: %d", got)
	}
}
