// Package textutil holds small string helpers shared across Miya.
package textutil

import "strings"

// FirstNonEmpty returns the first value that is not blank.
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ClipRunes shortens s to n runes, marking the cut with an ellipsis.
func ClipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n])) + "…"
	}
	return s
}

// FirstLine returns the trimmed first line of s.
func FirstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(l)
}

// Bounded keeps the last 8000 runes of long output text.
func Bounded(text string) string {
	r := []rune(text)
	if len(r) > 8000 {
		return "[earlier output omitted]\n" + string(r[len(r)-8000:])
	}
	return text
}

// LastLine is the last line of s that says something.
func LastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}
