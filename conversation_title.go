package main

// Named agent titles take precedence over a first-request preview. Retain the
// existing title when a partial server response has neither.
func conversationTitle(name, preview, existing string) string {
	return clippedTitle(firstNonEmpty(clippedTitle(name), clippedTitle(preview), clippedTitle(existing), "Untitled thread"))
}
