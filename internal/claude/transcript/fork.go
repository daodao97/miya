package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"miya/internal/appdir"
	"miya/internal/claude"
)

// forkClaudeSessionBefore writes the active branch of a session before its
// prompt-th user prompt (from 0) as a new session beside it, with new IDs,
// and returns the new session's ID.
func ForkBefore(path string, prompt int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	type line struct {
		e   Entry
		raw map[string]any
	}
	var entries []line
	for _, l := range bytes.Split(data, []byte("\n")) {
		var e Entry
		if json.Unmarshal(l, &e) != nil || e.UUID == "" || e.IsSidechain {
			continue
		}
		switch e.Type {
		case "user", "assistant", "system", "attachment", "progress":
		default:
			continue
		}
		var raw map[string]any
		if json.Unmarshal(l, &raw) != nil {
			continue
		}
		entries = append(entries, line{e, raw})
	}
	byUUID := make(map[string]int, len(entries))
	for i, l := range entries {
		byUUID[l.e.UUID] = i
	}
	var chain []line
	seen := map[string]bool{}
	for i := len(entries) - 1; i >= 0; {
		l := entries[i]
		if seen[l.e.UUID] {
			break
		}
		seen[l.e.UUID] = true
		chain = append(chain, l)
		parent, ok := byUUID[l.e.ParentUUID]
		if !ok {
			break
		}
		i = parent
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	cut, n := -1, 0
	for i, l := range chain {
		if l.e.Type != "user" || l.e.IsMeta {
			continue
		}
		var m struct {
			Content json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal(l.e.Message, &m)
		blocks := Blocks(m.Content)
		result := false
		for _, b := range blocks {
			result = result || b.Type == "tool_result"
		}
		if result || VisibleText(blocks) == "" && !HasImage(blocks) {
			continue
		}
		if n == prompt {
			cut = i
			break
		}
		n++
	}
	if cut <= 0 {
		return "", errors.New("This message was not found in Claude Code conversation history")
	}
	id := claude.NewSessionID()
	var out bytes.Buffer
	var parent any
	for _, l := range chain[:cut] {
		if l.e.Type == "progress" {
			continue
		}
		uuid := claude.NewSessionID()
		raw := l.raw
		raw["uuid"], raw["parentUuid"], raw["sessionId"], raw["isSidechain"] = uuid, parent, id, false
		if _, ok := raw["session_id"]; ok {
			raw["session_id"] = id
		}
		delete(raw, "logicalParentUuid")
		b, err := json.Marshal(raw)
		if err != nil {
			return "", err
		}
		out.Write(b)
		out.WriteByte('\n')
		parent = uuid
	}
	f, err := os.OpenFile(filepath.Join(filepath.Dir(path), id+".jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(out.Bytes()); err != nil {
		f.Close()
		return "", err
	}
	return id, f.Close()
}

// Sessions replaced by a rewind are kept by Claude Code but not listed.
var supersededMu sync.Mutex

func SupersededPath() string {
	dir := appdir.File("quickgui-claude")
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "superseded.json")
}

func LoadSuperseded() map[string]bool {
	supersededMu.Lock()
	defer supersededMu.Unlock()
	out := map[string]bool{}
	if data, err := os.ReadFile(SupersededPath()); err == nil {
		_ = json.Unmarshal(data, &out)
	}
	return out
}

func MarkSuperseded(id string) {
	path := SupersededPath()
	if path == "" || id == "" {
		return
	}
	supersededMu.Lock()
	defer supersededMu.Unlock()
	set := map[string]bool{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &set)
	}
	set[id] = true
	data, err := json.Marshal(set)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, data, 0o600)
}
