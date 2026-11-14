package nsui

import (
	"runtime"
	"testing"
	"time"

	"miya/internal/chat/model"
)

func TestStepThreadFollowsRecentActivity(t *testing.T) {
	now := time.Date(2026, 11, 14, 15, 0, 0, 0, time.Local)
	at := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	// Most recently active first: 4, 1, 2, 3.
	threads := []model.Thread{
		{ID: 1, LastActivityAt: at(2 * time.Hour)},
		{ID: 2, LastActivityAt: at(26 * time.Hour)},
		{ID: 3, Busy: true},
		{ID: 4, LastActivityAt: at(30 * time.Minute)},
	}
	for _, tc := range []struct{ current, step, want int }{
		{4, 1, 1}, {1, 1, 2}, {3, 1, 4}, {4, -1, 3}, {99, 1, 4}, {99, -1, 3},
	} {
		if got := stepThread(threads, tc.current, tc.step); got != tc.want {
			t.Errorf("step(%d, %d) = %d, want %d", tc.current, tc.step, got, tc.want)
		}
	}
	if got := stepThread(nil, 5, 1); got != 5 {
		t.Errorf("no threads: %d", got)
	}
}

// Shortcuts read as the platform writes them.
func TestKeysLabel(t *testing.T) {
	mod, alt := "Ctrl", "Alt"
	if runtime.GOOS == "darwin" {
		mod, alt = "⌘", "Option"
	}
	for keys, want := range map[string]string{
		"mod+shift+p":    mod + "+Shift+P",
		"mod+alt+b":      mod + "+" + alt + "+B",
		"ctrl+shift+tab": "Ctrl+Shift+Tab",
		"f1":             "F1",
		"escape":         "Esc",
	} {
		if got := keysLabel(keys); got != want {
			t.Errorf("keysLabel(%q) = %q, want %q", keys, got, want)
		}
	}
}
