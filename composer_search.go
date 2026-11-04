package main

import (
	"bufio"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type composerChoice struct {
	Name, Detail string
	ArgumentHint string     `json:"argumentHint,omitempty"`
	Ref          *ThreadRef `json:"ref,omitempty"`
	Agent        string     `json:"agent,omitempty"`
}

var composerCommands = []composerChoice{
	{Name: "goal", Detail: "Set a goal · /goal pause · /goal resume · /goal clear", ArgumentHint: "Describe a goal, or enter pause / resume / clear"},
	{Name: "new", Detail: "Start a new conversation"},
	{Name: "model", Detail: "Choose the model"},
	{Name: "fast", Detail: "Toggle Codex fast mode"},
	{Name: "permissions", Detail: "Change approval permissions"},
	{Name: "mention", Detail: "Reference a project file"},
	{Name: "review", Detail: "Review uncommitted changes"},
	{Name: "compact", Detail: "Compact this conversation"},
	{Name: "diff", Detail: "Show changes"},
	{Name: "status", Detail: "Show model, permissions and token usage"},
}

type composerToken struct {
	Kind       byte
	Start, End int // UTF-8 byte offsets into the draft
	Query      string
}

// Native input events expose the edited value, but not the caret. The end of
// the changed range identifies the token being edited, including mid-prompt
// insertions; unchanged text after that range is preserved on completion.
func composerEditEnd(before, after string) int {
	a, b := []rune(before), []rune(after)
	start := 0
	for start < len(a) && start < len(b) && a[start] == b[start] {
		start++
	}
	i, j := len(a), len(b)
	for i > start && j > start && a[i-1] == b[j-1] {
		i--
		j--
	}
	return len(string(b[:j]))
}

func composerTokenAt(value string, end int) composerToken {
	if end < 0 || end > len(value) {
		return composerToken{}
	}
	prefix := value[:end]
	if strings.HasPrefix(value, "/") && !strings.ContainsAny(prefix, " \t\r\n") {
		return composerToken{'/', 0, end, prefix[1:]}
	}
	start := strings.LastIndex(prefix, "@")
	if hash := strings.LastIndex(prefix, "#"); hash > start {
		boundary := hash == 0
		if hash > 0 {
			previous, _ := utf8.DecodeLastRuneInString(prefix[:hash])
			boundary = unicode.IsSpace(previous)
		}
		if boundary {
			start = hash
		}
	}
	if start < 0 {
		return composerToken{}
	}
	for _, m := range threadMentions(value) {
		if start >= m.Start && start < m.End {
			return composerToken{}
		}
	}
	for _, m := range agentMentions(value) {
		if start >= m.Start && start < m.End {
			return composerToken{}
		}
	}
	if referenceCodeMask(value)[start] {
		return composerToken{}
	}
	if start > 0 {
		r := []rune(prefix[:start])
		if !unicode.IsSpace(r[len(r)-1]) {
			return composerToken{}
		}
	}
	query := prefix[start+1:]
	if strings.HasPrefix(query, "\"") {
		query = query[1:]
		if strings.ContainsAny(query, "\"\r\n") {
			return composerToken{}
		}
	} else if strings.ContainsAny(query, " \t\r\n\"`") {
		return composerToken{}
	}
	if value[start] == '#' && len(agentChoices(query, nil)) == 0 {
		return composerToken{}
	}
	return composerToken{value[start], start, end, query}
}

func insertFileReference(value string, token composerToken, path string) string {
	if token.Kind != '@' || token.Start < 0 || token.End > len(value) {
		return value
	}
	path = filepath.ToSlash(path)
	if strings.ContainsAny(path, " \t\"\\") {
		path = "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(path) + "\""
	}
	return value[:token.Start] + "@" + path + " " + value[token.End:]
}

func slashCommand(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "/") {
		return "", false
	}
	// Absolute paths and multiline pasted text are ordinary messages.
	if strings.ContainsAny(value[1:], "/\r\n") {
		return "", false
	}
	return strings.TrimPrefix(value, "/"), true
}

func commandChoices(query string) []composerChoice {
	var out []composerChoice
	for _, cmd := range composerCommands {
		if strings.Contains(cmd.Name, strings.ToLower(query)) {
			out = append(out, cmd)
		}
	}
	return out
}

const maxProjectFiles = 50000

// rg respects gitignore, includes untracked files and never follows symlinks.
// The fallback keeps file references usable in packaged apps without rg.
func indexProjectFiles(ctx context.Context, root string) ([]string, error) {
	if _, err := exec.LookPath("rg"); err == nil {
		cmd := exec.CommandContext(ctx, "rg", "--files", "--hidden", "-0", "-g", "!.git", "-g", "!node_modules", "-g", "!.quickgui", "-g", "!dist", "-g", "!vendor")
		cmd.Dir = root
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		scan := bufio.NewScanner(stdout)
		scan.Split(func(data []byte, atEOF bool) (int, []byte, error) {
			if i := strings.IndexByte(string(data), 0); i >= 0 {
				return i + 1, data[:i], nil
			}
			if atEOF && len(data) > 0 {
				return len(data), data, nil
			}
			return 0, nil, nil
		})
		var files []string
		for scan.Scan() {
			files = append(files, scan.Text())
			if len(files) >= maxProjectFiles {
				_ = cmd.Process.Kill()
				break
			}
		}
		err = cmd.Wait()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if scan.Err() != nil {
			return nil, scan.Err()
		}
		if err == nil || len(files) > 0 {
			return files, nil
		}
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			return files, nil
		}
		return nil, err
	}
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if path == root {
				return err
			}
			return nil
		}
		if d.IsDir() {
			if path != root {
				switch d.Name() {
				case ".git", "node_modules", ".quickgui", "dist", "build", "vendor", "target", ".venv":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err == nil {
			files = append(files, filepath.ToSlash(rel))
		}
		if len(files) >= maxProjectFiles {
			return fs.SkipAll
		}
		return nil
	})
	return files, err
}

func fileChoices(files []string, query string) []composerChoice {
	query = strings.ToLower(filepath.ToSlash(query))
	type match struct {
		path  string
		score int
	}
	var matches []match
	for _, path := range files {
		lower := strings.ToLower(path)
		score := 0
		switch {
		case query == "":
			score = 3
		case lower == query:
			score = 0
		case strings.HasPrefix(strings.ToLower(filepath.Base(path)), query):
			score = 1
		case strings.Contains(lower, query):
			score = 2
		default:
			chars := []rune(query)
			i := 0
			for _, r := range lower {
				if i < len(chars) && r == chars[i] {
					i++
				}
			}
			if i != len(chars) {
				continue
			}
			score = 4
		}
		matches = append(matches, match{path, score})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score < matches[j].score
		}
		if len(matches[i].path) != len(matches[j].path) {
			return len(matches[i].path) < len(matches[j].path)
		}
		return matches[i].path < matches[j].path
	})
	if len(matches) > 30 {
		matches = matches[:30]
	}
	result := make([]composerChoice, 0, len(matches))
	for _, m := range matches {
		result = append(result, composerChoice{Name: m.path, Detail: filepath.Dir(m.path)})
	}
	return result
}

// fileMentionInputs converts completed @ references into app-server mention
// parts. The visible prompt is retained verbatim, while the structured input
// gives Codex the actual file path to read. Unknown @words stay ordinary text
// (useful for email addresses and prose).
func fileMentionInputs(text, root string) []map[string]any {
	var out []map[string]any
	for i := 0; i < len(text); i++ {
		if text[i] != '@' || (i > 0 && !unicode.IsSpace([]rune(text[:i])[len([]rune(text[:i]))-1])) {
			continue
		}
		start := i + 1
		end := start
		if start < len(text) && text[start] == '"' {
			start++
			end = start
			for end < len(text) && text[end] != '"' {
				if text[end] == '\\' && end+1 < len(text) {
					end += 2
				} else {
					end++
				}
			}
		} else {
			for end < len(text) {
				r, size := utf8.DecodeRuneInString(text[end:])
				if unicode.IsSpace(r) || (unicode.IsPunct(r) && !strings.ContainsRune("._-/\\", r)) {
					break
				}
				end += size
			}
		}
		if end <= start {
			continue
		}
		candidate := strings.ReplaceAll(text[start:end], `\"`, `"`)
		path := candidate
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, filepath.FromSlash(path))
		}
		path, _ = filepath.Abs(path)
		if _, err := os.Stat(path); err != nil {
			i = end
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			i = end
			continue
		}
		out = append(out, map[string]any{"type": "mention", "name": filepath.ToSlash(rel), "path": path})
		i = end
	}
	return out
}
