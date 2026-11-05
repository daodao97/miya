package main

import (
	"regexp"
	"strconv"
	"strings"
)

var diffHunkHeader = regexp.MustCompile(`^@@ -\d+(?:,(\d+))? \+\d+(?:,(\d+))? @@`)

// Count the full protocol diff before its displayed text is bounded. Hunk
// lengths distinguish file headers from changed lines beginning with +++/---.
func countDiffLines(diff string) DiffLineStats {
	var stats DiffLineStats
	var oldLeft, newLeft int
	for line := range strings.Lines(diff) {
		if h := diffHunkHeader.FindStringSubmatch(line); h != nil {
			oldLeft, newLeft = 1, 1
			if h[1] != "" {
				oldLeft, _ = strconv.Atoi(h[1])
			}
			if h[2] != "" {
				newLeft, _ = strconv.Atoi(h[2])
			}
			continue
		}
		inHunk := oldLeft > 0 || newLeft > 0
		switch {
		case strings.HasPrefix(line, "diff "):
			oldLeft, newLeft = 0, 0
		case !inHunk && (strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- ")):
			continue
		case strings.HasPrefix(line, "+"):
			stats.Added++
			if inHunk {
				newLeft--
			}
		case strings.HasPrefix(line, "-"):
			stats.Deleted++
			if inHunk {
				oldLeft--
			}
		case inHunk && strings.HasPrefix(line, " "):
			oldLeft--
			newLeft--
		}
	}
	return stats
}

// The latest turn diff already includes its fileChange items. Fall back to
// completed file changes for history, where turn/diff events are unavailable.
func turnDiffStats(messages []Message) DiffLineStats {
	type turnStats struct {
		diff, files DiffLineStats
		hasDiff     bool
	}
	turns := map[string]turnStats{}
	for _, m := range messages {
		s := turns[m.TurnID]
		switch strings.ToLower(m.Kind) {
		case "diff":
			s.hasDiff = true
			if m.DiffStats != nil {
				s.diff = *m.DiffStats
			} else {
				s.diff = countDiffLines(m.Content)
			}
		case "filechange":
			if m.Status != "completed" || m.Item == nil {
				continue
			}
			for _, change := range m.Item.Changes {
				stats := countDiffLines(change.Diff)
				s.files.Added += stats.Added
				s.files.Deleted += stats.Deleted
			}
		}
		turns[m.TurnID] = s
	}
	var total DiffLineStats
	for _, s := range turns {
		stats := s.files
		if s.hasDiff {
			stats = s.diff
		}
		total.Added += stats.Added
		total.Deleted += stats.Deleted
	}
	return total
}
