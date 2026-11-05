package main

import (
	"miya/internal/textutil"
)

// Named agent titles take precedence over a first-request preview. Retain the
// existing title when a partial server response has neither.
func conversationTitle(name, preview, existing string) string {
	return clippedTitle(textutil.FirstNonEmpty(clippedTitle(name), clippedTitle(preview), clippedTitle(existing), "Untitled thread"))
}
