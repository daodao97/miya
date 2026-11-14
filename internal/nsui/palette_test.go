package nsui

import (
	"testing"
	"time"

	"miya/internal/chat/model"
)

func TestPaletteMatches(t *testing.T) {
	for _, tc := range []struct {
		text, query string
		want        bool
	}{
		{"Open settings", "open settings", true},
		{"Open settings", "stng", true},
		{"Open settings", "sett open", true},
		{"Open settings", "gnits", false},
		{"New conversation", "", true},
	} {
		if got := paletteMatches(tc.text, tc.query); got != tc.want {
			t.Errorf("paletteMatches(%q, %q) = %v", tc.text, tc.query, got)
		}
	}
}

func TestPaletteEntries(t *testing.T) {
	now := time.Date(2026, 11, 14, 15, 0, 0, 0, time.Local)
	var picked int
	noop := func() {}
	cmd := commands{newThread: noop, openSettings: noop, toggleSidebar: noop, stop: noop, openPalette: noop}
	threads := []model.Thread{{ID: 7, Title: "Fix build", Project: "miya", LastActivityAt: now.UnixMilli()}}
	ids := func(current model.Thread) map[string]paletteEntry {
		out := map[string]paletteEntry{}
		for _, e := range paletteEntries(cmd, current, threads, func(t model.Thread) { picked = t.ID }, now) {
			out[e.item.ID] = e
		}
		return out
	}
	idle := ids(model.Thread{ID: 7})
	if _, ok := idle["cmd:stop"]; ok {
		t.Error("Stop offered while nothing runs")
	}
	if _, ok := ids(model.Thread{ID: 7, Busy: true})["cmd:stop"]; !ok {
		t.Error("Stop missing while the turn runs")
	}
	e, ok := idle["thread:7"]
	if !ok || e.item.Label != "Fix build" || e.item.Description != "miya" && e.item.Description[:4] != "miya" {
		t.Fatalf("conversation entry = %+v", e.item)
	}
	e.run()
	if picked != 7 {
		t.Fatalf("selected %d, want 7", picked)
	}
}
