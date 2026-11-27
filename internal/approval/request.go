package approval

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"miya/internal/textutil"
)

// Request is an approval request as the agent sent it.
type Request struct {
	// Tool is Claude Code's tool name, Input its input; Tool is empty for a
	// Codex request.
	Tool  string
	Input json.RawMessage
	// Command is what the request asks for, for the record.
	Command string
}

// BashCommand is the command of a Claude Bash request.
func BashCommand(ask Request) string {
	var in struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(ask.Input, &in)
	return in.Command
}

// Classify says whether a request stays in dir, the directory the agent
// works in, and names what it asks for.
func Classify(dir string, ask Request) (bool, string) {
	if ask.Tool == "" {
		// Codex asks only for what its workspace sandbox does not allow.
		return false, textutil.FirstNonEmpty(strings.TrimSpace(ask.Command), "an action outside the sandbox")
	}
	var in struct {
		Command      string `json:"command"`
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		URL          string `json:"url"`
	}
	_ = json.Unmarshal(ask.Input, &in)
	switch ask.Tool {
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		path := textutil.FirstNonEmpty(in.FilePath, in.NotebookPath)
		return path != "" && within(dir, path), ask.Tool + " " + path
	case "Bash":
		return in.Command != "" && !OutsideCommand(in.Command), in.Command
	}
	return false, strings.TrimSpace(ask.Tool + " " + in.URL)
}

// within reports whether path is dir or below it.
func within(dir, path string) bool {
	if dir == "" {
		return false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
