// Package composer understands what is typed in a message composer: slash
// commands, @ file and # agent references, the project file index behind them,
// and the collaboration instructions agent tags add to native input.
package composer

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

	"miya/internal/chat/model"
	"miya/locales"
)

var ComposerCommands = []model.ComposerChoice{
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

type ComposerToken struct {
	Kind       byte
	Start, End int // UTF-8 byte offsets into the draft
	Query      string
}

// Native input events expose the edited value, but not the caret. The end of
// the changed range identifies the token being edited, including mid-prompt
// insertions; unchanged text after that range is preserved on completion.
func ComposerEditEnd(before, after string) int {
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

func ComposerTokenAt(value string, end int) ComposerToken {
	if end < 0 || end > len(value) {
		return ComposerToken{}
	}
	prefix := value[:end]
	if strings.HasPrefix(value, "/") && !strings.ContainsAny(prefix, " \t\r\n") {
		return ComposerToken{'/', 0, end, prefix[1:]}
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
		return ComposerToken{}
	}
	for _, m := range model.ThreadMentions(value) {
		if start >= m.Start && start < m.End {
			return ComposerToken{}
		}
	}
	for _, m := range model.AgentMentions(value) {
		if start >= m.Start && start < m.End {
			return ComposerToken{}
		}
	}
	if model.ReferenceCodeMask(value)[start] {
		return ComposerToken{}
	}
	if start > 0 {
		r := []rune(prefix[:start])
		if !unicode.IsSpace(r[len(r)-1]) {
			return ComposerToken{}
		}
	}
	query := prefix[start+1:]
	if strings.HasPrefix(query, "\"") {
		query = query[1:]
		if strings.ContainsAny(query, "\"\r\n") {
			return ComposerToken{}
		}
	} else if strings.ContainsAny(query, " \t\r\n\"`") {
		return ComposerToken{}
	}
	if value[start] == '#' && len(AgentChoices(query, nil)) == 0 {
		return ComposerToken{}
	}
	return ComposerToken{value[start], start, end, query}
}

func InsertFileReference(value string, token ComposerToken, path string) string {
	if token.Kind != '@' || token.Start < 0 || token.End > len(value) {
		return value
	}
	path = filepath.ToSlash(path)
	if strings.ContainsAny(path, " \t\"\\") {
		path = "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(path) + "\""
	}
	return value[:token.Start] + "@" + path + " " + value[token.End:]
}

func SlashCommand(value string) (string, bool) {
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

func CommandChoices(query string) []model.ComposerChoice {
	var out []model.ComposerChoice
	for _, cmd := range ComposerCommands {
		if strings.Contains(cmd.Name, strings.ToLower(query)) {
			out = append(out, cmd)
		}
	}
	return out
}

const MaxProjectFiles = 50000

// rg respects gitignore, includes untracked files and never follows symlinks.
// The fallback keeps file references usable in packaged apps without rg.
func IndexProjectFiles(ctx context.Context, root string) ([]string, error) {
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
			if len(files) >= MaxProjectFiles {
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
		if len(files) >= MaxProjectFiles {
			return fs.SkipAll
		}
		return nil
	})
	return files, err
}

func FileChoices(files []string, query string) []model.ComposerChoice {
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
	result := make([]model.ComposerChoice, 0, len(matches))
	for _, m := range matches {
		result = append(result, model.ComposerChoice{Name: m.path, Detail: filepath.Dir(m.path)})
	}
	return result
}

// fileMentionInputs converts completed @ references into app-server mention
// parts. The visible prompt is retained verbatim, while the structured input
// gives Codex the actual file path to read. Unknown @words stay ordinary text
// (useful for email addresses and prose).
func FileMentionInputs(text, root string) []map[string]any {
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

func AgentReferenceText(agent, title string) string {
	uri := model.AgentURL(agent)
	if uri == "" {
		return ""
	}
	if title == "" {
		title = model.AgentName(agent)
	}
	title = strings.Join(strings.Fields(title), " ")
	title = strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`).Replace(title)
	return "#[" + title + "](" + uri + ")"
}

func AgentChoices(query string, allowed []string) []model.ComposerChoice {
	query = strings.ToLower(query)
	var out []model.ComposerChoice
	for _, id := range model.Backends {
		if allowed != nil {
			found := false
			for _, allowedID := range allowed {
				if id == allowedID {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		aliases := id + " " + model.AgentName(id)
		if id == model.AgentCodex {
			aliases += " openai"
		}
		if id == model.AgentClaude {
			aliases += " claude anthropic"
		}
		if !strings.Contains(strings.ToLower(aliases), query) {
			continue
		}
		out = append(out, model.ComposerChoice{Name: model.AgentName(id), Agent: id, Detail: tr("Run a task in an independent conversation")})
	}
	return out
}

func InsertAgentReference(value string, token ComposerToken, agent, title string) string {
	if token.Kind != '#' && token.Kind != '@' || token.Start < 0 || token.End > len(value) || token.End < token.Start {
		return value
	}
	if tag := AgentReferenceText(agent, title); tag != "" {
		return value[:token.Start] + tag + " " + value[token.End:]
	}
	return value
}

// Instructions are added only to native input. The user's visible message,
// structured parts, draft and memory evidence retain exactly their own text.
func CollaborationTaggedInput(text string, parts []model.MessagePart) string {
	var mentions []model.AgentMention
	for _, part := range parts {
		if part.Type == "agent_ref" && model.RegisteredBackend(part.Agent) {
			mentions = append(mentions, model.AgentMention{Agent: part.Agent})
		}
	}
	if len(mentions) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(text)
	b.WriteString("\n\n<miya_collaboration_v1>\nAgent tags identify the user's requested collaboration targets by canonical backend ID. Use Miya start_task to delegate the relevant work in independent sessions, then read_task/wait_task to collect fixed results and continue the user's task. Selecting a tag did not start a task. Use a unique request_id per operation and preserve it on retry. Default to context none and notify on_finish so completion resumes you; use notify none only for explicitly record-only work. While actively waiting, poll wait_task; do not leave delegated work untracked. @conversation references remain context only. Targets:\n")
	seen := map[string]bool{}
	for _, m := range mentions {
		if !seen[m.Agent] {
			b.WriteString("- " + m.Agent + "\n")
			seen[m.Agent] = true
		}
	}
	b.WriteString("</miya_collaboration_v1>")
	return b.String()
}

func tr(s string) string { return locales.T(s) }
