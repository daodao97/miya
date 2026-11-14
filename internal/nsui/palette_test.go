package nsui

import (
	"testing"

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
	var picked int
	noop := func() {}
	cmd := commands{newThread: noop, openSettings: noop, toggleSidebar: noop, stop: noop, openPalette: noop, shortcuts: noop}
	refs := threadChoices([]model.ProjectSnapshot{{Name: "miya", Path: "/w/miya", Threads: []model.Thread{{ID: 7, Title: "Fix build", LastActivityAt: 1}}}})
	ids := func(current model.Thread) map[string]paletteEntry {
		out := map[string]paletteEntry{}
		for _, e := range paletteEntries(cmd, current, refs, func(t model.Thread) { picked = t.ID }) {
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
