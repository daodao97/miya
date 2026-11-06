package text

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Images cross the native bridge as file URLs, never large base64 strings.
func LocalImageURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return (&url.URL{Scheme: "file", Path: abs}).String()
}

func MessageLabel(kind string) string {
	labels := map[string]string{"reasoning": tr("Thinking"), "plan": tr("Plan"), "commandexecution": tr("Command"), "filechange": tr("File changes"), "diff": tr("Changes"), "mcptoolcall": tr("MCP tool"), "dynamictoolcall": tr("Tool"), "collabagenttoolcall": tr("Agent task"), "subagentactivity": tr("Agent activity"), "websearch": tr("Web search"), "imageview": tr("Image"), "imagegeneration": tr("Generated image"), "enteredreviewmode": tr("Review started"), "exitedreviewmode": tr("Review completed"), "contextcompaction": tr("Context compacted"), "sleep": tr("Waiting"), "functioncalloutput": tr("Tool result"), "hookprompt": tr("Hook"), "handoff": tr("Handoff")}
	if label := labels[strings.ToLower(kind)]; label != "" {
		return label
	}
	return tr("Activity")
}
