package main

import (
	"regexp"
	"strings"
)

var claudeCommandTitleParts = regexp.MustCompile(`(?s)<command-(message|name|args)>(.*?)</command-(?:message|name|args)>`)

// Claude's native transcript wraps slash commands in protocol tags. Use the
// invocation as the title, without changing the stored prompt or its display.
func claudePromptTitle(text string) string {
	parts := claudeCommandTitleParts.FindAllStringSubmatch(text, -1)
	if len(parts) == 0 {
		return clippedTitle(text)
	}
	var message, name, args string
	for _, part := range parts {
		switch part[1] {
		case "message":
			message = strings.TrimSpace(part[2])
		case "name":
			name = strings.TrimSpace(part[2])
		case "args":
			args = strings.TrimSpace(part[2])
		}
	}
	name = firstNonEmpty(name, message)
	if name != "" && !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	rest := strings.TrimSpace(claudeCommandTitleParts.ReplaceAllString(text, ""))
	return clippedTitle(strings.Join([]string{name, args, rest}, " "))
}
