// Package transcript reads Claude Code's session transcripts under its
// config directory: listing, summarizing, parsing them into conversation
// messages, and forking one before a prompt.
package transcript

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"miya/internal/claude"
	"miya/internal/memory"
	"miya/internal/model"
	"miya/internal/subagent"
	"miya/internal/textutil"
	"miya/locales"
)

const SummaryReadLimit = 256 << 10

// claudeConfigOverride is the CLAUDE_CONFIG_DIR of the Claude Code command.
var ConfigOverride string

func ConfigDir() string {
	if ConfigOverride != "" {
		return ConfigOverride
	}
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// claudeProjectDir is where Claude Code keeps a working directory's sessions:
// its path with every character but letters and digits made a dash.
func ProjectDir(cwd string) string {
	root := ConfigDir()
	if root == "" || cwd == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range cwd {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return filepath.Join(root, "projects", b.String())
}

type Session struct {
	ID, Title, Cwd string
	UpdatedAt      time.Time
	Path           string
}

// claudeEntry is the part of a line the app reads.
type Entry struct {
	Type          string          `json:"type"`
	UUID          string          `json:"uuid"`
	ParentUUID    string          `json:"parentUuid"`
	SessionID     string          `json:"sessionId"`
	Cwd           string          `json:"cwd"`
	Timestamp     string          `json:"timestamp"`
	IsSidechain   bool            `json:"isSidechain"`
	IsMeta        bool            `json:"isMeta"`
	AITitle       string          `json:"aiTitle"`
	CustomTitle   string          `json:"customTitle"`
	Summary       string          `json:"summary"`
	Message       json.RawMessage `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	Subtype       string          `json:"subtype"`
	TaskID        string          `json:"task_id"`
	ToolUseID     string          `json:"tool_use_id"`
	Status        json.RawMessage `json:"status"`
	Patch         json.RawMessage `json:"patch"`
}

type Block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Source    *ImageSource    `json:"source"`
}

// claudeBlocks reads message content, which is a string or a list of blocks.
func Blocks(raw json.RawMessage) []Block {
	if len(raw) == 0 {
		return nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []Block{{Type: "text", Text: text}}
	}
	var blocks []Block
	_ = json.Unmarshal(raw, &blocks)
	return blocks
}

// claudeVisibleText joins the text a user wrote, leaving out the context
// the CLI adds to a prompt.
func VisibleText(blocks []Block) string {
	var parts []string
	for _, b := range blocks {
		if b.Type != "text" {
			continue
		}
		text := strings.TrimSpace(memory.StripContext(b.Text))
		if text == "" || strings.HasPrefix(text, "[Request interrupted by user") || strings.HasPrefix(text, "<system-reminder>") || strings.HasPrefix(text, "<local-command-") {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n")
}

func ParseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// readClaudeLines reads a session's lines, as long as they are, up to limit
// bytes when limit is positive.
func ReadLines(path string, limit int64, fn func(Entry) bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	if limit > 0 {
		r = io.LimitReader(f, limit)
	}
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			var e Entry
			if json.Unmarshal(line, &e) == nil && !fn(e) {
				return nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func ClippedTitle(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 45 {
		return string(r[:45]) + "…"
	}
	return s
}

// summarizeClaudeSession reads the start of a session for its identity and
// title, without reading the whole file.
func Summarize(path string) (Session, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return Session{}, false
	}
	s := Session{ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Path: path, UpdatedAt: info.ModTime()}
	var prompt, aiTitle, custom, summary string
	visit := func(e Entry) bool {
		if s.Cwd == "" && e.Cwd != "" {
			s.Cwd = e.Cwd
		}
		switch e.Type {
		case "summary":
			summary = e.Summary
		case "ai-title":
			aiTitle = e.AITitle
		case "custom-title":
			custom = e.CustomTitle
		case "user":
			if prompt == "" && !e.IsMeta && !e.IsSidechain {
				var m struct {
					Content json.RawMessage `json:"content"`
				}
				_ = json.Unmarshal(e.Message, &m)
				prompt = PromptTitle(VisibleText(Blocks(m.Content)))
			}
		}
		return true
	}
	_ = ReadLines(path, SummaryReadLimit, visit)
	// Recent generated titles can be far beyond the initial prompt.
	if info.Size() > SummaryReadLimit {
		if f, err := os.Open(path); err == nil {
			defer f.Close()
			_, _ = f.Seek(max(SummaryReadLimit, info.Size()-(128<<10)), io.SeekStart)
			br := bufio.NewReader(io.LimitReader(f, 128<<10))
			_, _ = br.ReadBytes('\n')
			for {
				line, err := br.ReadBytes('\n')
				var e Entry
				if json.Unmarshal(line, &e) == nil && (e.Type == "custom-title" || e.Type == "ai-title" || e.Type == "summary") {
					visit(e)
				}
				if err != nil {
					break
				}
			}
		}
	}
	if prompt == "" && aiTitle == "" && custom == "" && summary == "" {
		// Sessions without a prompt hold nothing to show.
		return Session{}, false
	}
	s.Title = ClippedTitle(textutil.FirstNonEmpty(custom, ReferenceTitle(aiTitle), ReferenceTitle(summary), prompt, "Claude Code conversation"))
	return s, true
}

// listClaudeSessions finds the CLI's sessions of a working directory, the
// latest first.
func List(cwd string) []Session {
	dir := ProjectDir(cwd)
	if dir == "" {
		return nil
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	superseded := LoadSuperseded()
	var out []Session
	for _, path := range paths {
		if superseded[strings.TrimSuffix(filepath.Base(path), ".jsonl")] {
			continue
		}
		s, ok := Summarize(path)
		// The directory name is lossy: two folders can share it.
		if !ok || s.Cwd != "" && filepath.Clean(s.Cwd) != filepath.Clean(cwd) {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

// findClaudeSession is a session's file: under its working directory, else
// under any project.
func Find(cwd, id string) string {
	if id == "" || filepath.Base(id) != id {
		return ""
	}
	if dir := ProjectDir(cwd); dir != "" {
		if p := filepath.Join(dir, id+".jsonl"); fileExists(p) {
			return p
		}
	}
	if root := ConfigDir(); root != "" {
		if matches, _ := filepath.Glob(filepath.Join(root, "projects", "*", id+".jsonl")); len(matches) > 0 {
			return matches[0]
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// parseClaudeSession reads the branch of a session the CLI shows: from its
// last entry back through their parents, side chains left out.
func Parse(path string) ([]model.Message, error) {
	var entries []Entry
	err := ReadLines(path, 0, func(e Entry) bool {
		if e.UUID == "" || e.IsSidechain {
			return true
		}
		switch e.Type {
		case "user", "assistant", "system", "progress", "attachment":
			// Only the parts read later are kept.
			if e.Type != "user" && e.Type != "assistant" {
				e.Message = nil
			}
			entries = append(entries, e)
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	byUUID := make(map[string]int, len(entries))
	for i, e := range entries {
		byUUID[e.UUID] = i
	}
	var chain []Entry
	seen := map[string]bool{}
	for i := len(entries) - 1; i >= 0; {
		e := entries[i]
		if seen[e.UUID] {
			break
		}
		seen[e.UUID] = true
		chain = append(chain, e)
		parent, ok := byUUID[e.ParentUUID]
		if !ok {
			break
		}
		i = parent
	}
	var out []model.Message
	tools := map[string]int{}
	var turnStart int64
	for k := len(chain) - 1; k >= 0; k-- {
		e := chain[k]
		at := ParseTime(e.Timestamp).UnixMilli()
		var m struct {
			ID      string          `json:"id"`
			Content json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal(e.Message, &m)
		blocks := Blocks(m.Content)
		switch e.Type {
		case "system":
			TaskFrame(&model.Thread{Messages: out}, claude.Frame{Subtype: e.Subtype, TaskID: e.TaskID, ToolUseID: e.ToolUseID, Status: e.Status, Patch: e.Patch, Summary: e.Summary})
		case "user":
			results := false
			for _, b := range blocks {
				if b.Type == "text" {
					if f, ok := claude.TaskNotification(b.Text); ok {
						TaskFrame(&model.Thread{Messages: out}, f)
						results = true
					}
				}
				if b.Type != "tool_result" {
					continue
				}
				results = true
				if i, ok := tools[b.ToolUseID]; ok {
					out[i].Status = "completed"
					if b.IsError {
						out[i].Status = "failed"
					}
					output := VisibleText(Blocks(b.Content))
					if shown := ToolOutput(out[i], output); shown != "" {
						out[i].Content += "\n" + textutil.Bounded(shown)
					}
					out[i].CompletedAtMs = at
					FinishSubAgents(&out[i], output, e.ToolUseResult)
				}
			}
			if results || e.IsMeta {
				continue
			}
			if text, images := VisibleText(blocks), Images(blocks); text != "" || len(images) > 0 {
				turnStart = at
				own, _ := model.SplitReferenceContext(text)
				out = append(out, model.Message{Role: "user", Content: text, Parts: model.MessageParts(own), Images: images, ItemID: e.UUID, SentAtMs: at})
			}
		case "assistant":
			for i, b := range blocks {
				item := e.UUID + ":" + strconv.Itoa(i)
				switch b.Type {
				case "text":
					if strings.TrimSpace(b.Text) != "" {
						out = append(out, model.Message{Role: "assistant", Kind: "agentMessage", Status: "completed", Content: b.Text, ItemID: item, StartedAtMs: turnStart, CompletedAtMs: at})
					}
				case "thinking":
					if b.Thinking != "" {
						out = append(out, model.Message{Role: "activity", Kind: "reasoning", Status: "completed", Content: b.Thinking, ItemID: item})
					}
				case "tool_use":
					tools[b.ID] = len(out)
					out = append(out, model.Message{Role: "tool", Kind: "claudeTool", Status: "completed", Content: b.Name + "\n" + string(b.Input), ItemID: b.ID, StartedAtMs: turnStart, CompletedAtMs: at, Agents: SubAgents(b.ID, b.Name, b.Input, "inProgress")})
				}
			}
		}
	}
	// A historical launch acknowledgement is not evidence of completion, and
	// a history read cannot establish whether another process still owns it.
	for i := range out {
		for j := range out[i].Agents {
			a := &out[i].Agents[j]
			if subagent.Active(a.Status) {
				a.Status, a.Detail = subagent.Unconfirmed, "No subtask completion notification in history"
			}
		}
	}
	return out, nil
}

func tr(s string) string { return locales.T(s) }
