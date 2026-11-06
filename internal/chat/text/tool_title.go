package text

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"miya/internal/chat/model"
	"miya/internal/textutil"
)

// claudeToolCall reads a Claude tool activity, whose content is the tool's
// name, its input as JSON, then its output.
//
// Views call it for every tool row in every frame, so the decoded input is
// kept by its text: the returned map is shared and must not be changed.
func ToolCall(m model.Message) (string, map[string]any) {
	name, rest, _ := strings.Cut(m.Content, "\n")
	return strings.TrimSpace(name), decodeToolInput(rest)
}

var toolInputs struct {
	sync.Mutex
	m map[string]map[string]any
}

func decodeToolInput(rest string) map[string]any {
	toolInputs.Lock()
	input, ok := toolInputs.m[rest]
	toolInputs.Unlock()
	if ok {
		return input
	}
	_ = json.NewDecoder(strings.NewReader(rest)).Decode(&input)
	toolInputs.Lock()
	if toolInputs.m == nil || len(toolInputs.m) >= 512 {
		toolInputs.m = map[string]map[string]any{}
	}
	toolInputs.m[rest] = input
	toolInputs.Unlock()
	return input
}

func InputString(input map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := input[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// shortPath shows a path under the home directory with ~.
func ShortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") && filepath.IsAbs(p) {
			return "~/" + rel
		}
	}
	return p
}

// claudeToolTitle previews a Claude tool call as Codex activities show
// theirs: what ran, what was read or edited.
func ToolTitle(m model.Message) string {
	name, input := ToolCall(m)
	active := m.Status == "inProgress" || m.Status == "in_progress"
	verb := func(done, running string) string {
		switch {
		case active:
			return running
		case m.Status == "failed":
			return tr("Failed · ")
		}
		return done
	}
	var detail, prefix string
	switch name {
	case "Bash":
		prefix, detail = verb(tr("Ran "), tr("Running ")), textutil.FirstLine(InputString(input, "command", "description"))
	case "Read", "NotebookRead":
		prefix, detail = verb(tr("Read "), tr("Reading ")), ShortPath(InputString(input, "file_path", "notebook_path", "path"))
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		prefix, detail = verb(tr("Edited")+" ", tr("Editing")+" "), ShortPath(InputString(input, "file_path", "notebook_path"))
	case "Grep":
		prefix, detail = verb(tr("Searched "), tr("Searching ")), InputString(input, "pattern")
	case "Glob", "LS":
		prefix, detail = verb(tr("Listed "), tr("Listing ")), InputString(input, "pattern", "path")
	case "WebFetch":
		prefix, detail = verb(tr("Visited "), tr("Visiting ")), InputString(input, "url")
	case "WebSearch":
		prefix, detail = verb(tr("Searched "), tr("Searching ")), InputString(input, "query")
	case "Task", "Agent":
		prefix, detail = verb(tr("Subtask · "), tr("Running subtask · ")), InputString(input, "description", "prompt")
	case "TodoWrite":
		return verb(tr("Updated tasks"), tr("Updating tasks"))
	}
	if detail != "" {
		return prefix + textutil.FirstLine(detail)
	}
	// Other tools: their name and their first text argument.
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := input[k].(string); ok && strings.TrimSpace(s) != "" {
			return name + " · " + textutil.FirstLine(s)
		}
	}
	return textutil.FirstNonEmpty(name, MessageLabel(m.Kind))
}
