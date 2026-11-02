package main

import (
	"regexp"
	"strings"
)

var claudeCommandTitleParts = regexp.MustCompile(`(?s)<command-(message|name|args)>(.*?)</command-(?:message|name|args)>`)

// Claude's native transcript wraps slash commands in protocol tags. Use the
// invocation as the title, without changing the stored prompt or its display.
func claudePromptTitle(text string) string {
	text = claudeReferenceTitle(text)
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

// Leading conversation references provide context; the request following
// them is a better preview. Reference-only requests fall back to their
// readable labels. Preserve the original prompt and never resolve a link.
func claudeReferenceTitle(text string) string {
	text, _ = splitReferenceContext(text)
	mentions := threadMentions(text)
	if len(mentions) == 0 {
		return text
	}
	leadingEnd := 0
	for _, mention := range mentions {
		if strings.Trim(text[leadingEnd:mention.Start], " \t\r\n,，、;；") != "" {
			break
		}
		leadingEnd = mention.End
	}
	if request := strings.TrimLeft(text[leadingEnd:], " \t\r\n,，、;；：:"); leadingEnd > 0 && request != "" {
		text = request
		mentions = threadMentions(text)
	}
	var title strings.Builder
	end := 0
	for _, mention := range mentions {
		title.WriteString(text[end:mention.Start])
		title.WriteString(firstNonEmpty(strings.TrimSpace(mention.Title), "引用对话"))
		end = mention.End
	}
	title.WriteString(text[end:])
	return title.String()
}
