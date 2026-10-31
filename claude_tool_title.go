package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// claudeToolCall reads a Claude tool activity, whose content is the tool's
// name, its input as JSON, then its output.
func claudeToolCall(m Message) (string, map[string]any) {
	name, rest, _ := strings.Cut(m.Content, "\n")
	var input map[string]any
	_ = json.NewDecoder(strings.NewReader(rest)).Decode(&input)
	return strings.TrimSpace(name), input
}

func inputString(input map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := input[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(l)
}

// shortPath shows a path under the home directory with ~.
func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") && filepath.IsAbs(p) {
			return "~/" + rel
		}
	}
	return p
}

// claudeToolTitle previews a Claude tool call as Codex activities show
// theirs: what ran, what was read or edited.
func claudeToolTitle(m Message) string {
	name, input := claudeToolCall(m)
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
		prefix, detail = verb(tr("Ran "), tr("Running ")), firstLine(inputString(input, "command", "description"))
	case "Read", "NotebookRead":
		prefix, detail = verb("已读取 ", "正在读取 "), shortPath(inputString(input, "file_path", "notebook_path", "path"))
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		prefix, detail = verb(tr("Edited")+" ", tr("Editing")+" "), shortPath(inputString(input, "file_path", "notebook_path"))
	case "Grep":
		prefix, detail = verb("已搜索 ", "正在搜索 "), inputString(input, "pattern")
	case "Glob", "LS":
		prefix, detail = verb("已列出 ", "正在列出 "), inputString(input, "pattern", "path")
	case "WebFetch":
		prefix, detail = verb("已访问 ", "正在访问 "), inputString(input, "url")
	case "WebSearch":
		prefix, detail = verb("已搜索 ", "正在搜索 "), inputString(input, "query")
	case "Task", "Agent":
		prefix, detail = verb("子任务 · ", "正在执行子任务 · "), inputString(input, "description", "prompt")
	case "TodoWrite":
		return verb("已更新待办", "正在更新待办")
	}
	if detail != "" {
		return prefix + firstLine(detail)
	}
	// Other tools: their name and their first text argument.
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := input[k].(string); ok && strings.TrimSpace(s) != "" {
			return name + " · " + firstLine(s)
		}
	}
	return firstNonEmpty(name, messageLabel(m.Kind))
}

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
