package main

import (
	"strings"
)

// activityIcon is a Claude tool's own icon, else its kind's.
func activityIcon(m Message) string {
	if !strings.EqualFold(m.Kind, "claudeTool") {
		return messageIcon(m.Kind)
	}
	name, _ := claudeToolCall(m)
	switch name {
	case "Bash":
		return "terminal-2"
	case "Read", "NotebookRead":
		return "file-text"
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		return "edit"
	case "Grep", "Glob", "LS":
		return "folder"
	case "WebFetch", "WebSearch":
		return "globe"
	case "Task", "Agent":
		return "stack-2"
	}
	return messageIcon(m.Kind)
}
