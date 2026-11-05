package main

import (
	"miya/internal/textutil"
	"regexp"
	"strings"
)

var claudeCommandTitleParts = regexp.MustCompile(`(?s)<command-(message|name|args)>(.*?)</command-(?:message|name|args)>`)

// Claude's native transcript wraps slash commands in protocol tags. Use the
// invocation as the title, without changing the stored prompt.
func claudePromptTitle(text string) string {
	return clippedTitle(claudeCommandText(claudeReferenceTitle(text)))
}

// Normalize only a leading native command envelope. Literal XML examples in
// prose or code remain untouched, and command arguments keep their full length.
func claudeCommandText(text string) string {
	if !strings.HasPrefix(strings.TrimSpace(text), "<command-") {
		return text
	}
	parts := claudeCommandTitleParts.FindAllStringSubmatch(text, -1)
	if len(parts) == 0 {
		return text
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
	name = textutil.FirstNonEmpty(name, message)
	if name != "" && !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	rest := strings.TrimSpace(claudeCommandTitleParts.ReplaceAllString(text, ""))
	var invocation []string
	for _, part := range []string{name, args, rest} {
		if part != "" {
			invocation = append(invocation, part)
		}
	}
	return strings.Join(invocation, " ")
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
		title.WriteString(textutil.FirstNonEmpty(strings.TrimSpace(mention.Title), "Referenced conversation"))
		end = mention.End
	}
	title.WriteString(text[end:])
	return title.String()
}
