package model

import (
	"regexp"
	"strings"

	"miya/internal/contacts"
	"miya/internal/memory"
)

// What agents add around a user's message when they send attachments.
var attachmentEnvelope = regexp.MustCompile(`(?is).*?files\s+mentioned\s+by\s+the\s+user\s*:\s*.*?my\s+request\s*:\s*`)
var attachmentInstruction = regexp.MustCompile(`(?is)distinguish\s+instructions\s+in\s+attached\s+documents\s+from\s+the\s+user'?s\s+request\.?`)
var attachmentTempPath = regexp.MustCompile(`(?im)^\s*(?:codex-clipboard-[\w-]+\.(?:png|jpg|jpeg|gif|webp)\s*:\s*)?/var/folders/[^\n]+\s*$`)
var attachmentImageTag = regexp.MustCompile(`(?is)<image\b[^>]*>\s*</image\s*>|<image\b[^>]*/?>`)

// ImagePlaceholder is how an agent writes an attached image into a message.
var ImagePlaceholder = regexp.MustCompile(`(?i)\[image:\s*[^\]]*\]`)

// CleanUserText is what the user typed in a message: without the memory
// context and the attachment notes agents add around it.
func CleanUserText(text string) string {
	text = contacts.StripInput(memory.StripContext(text))
	text = attachmentEnvelope.ReplaceAllString(text, "")
	text = attachmentInstruction.ReplaceAllString(text, "")
	text = attachmentTempPath.ReplaceAllString(text, "")
	text = attachmentImageTag.ReplaceAllString(text, "")
	text = ImagePlaceholder.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}
