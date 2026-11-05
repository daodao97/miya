package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"miya/internal/appdir"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Command is how Claude Code starts: its executable, the options put before
// the app's own, and variables added to its environment.
type Command struct {
	Path string
	Args []string
	Env  []string
}

// ParseCommand reads a command line as a shell would split it: leading
// NAME=value words are variables, the next word the executable (claude when
// there is none), the rest its options. Quotes and backslashes group words,
// and ~/ at the start of a word or after its = is the home directory.
func ParseCommand(line string) (Command, error) {
	words, err := splitWords(line)
	if err != nil {
		return Command{}, err
	}
	var cmd Command
	i := 0
	for ; i < len(words) && isAssignment(words[i]); i++ {
		cmd.Env = append(cmd.Env, expandHome(words[i]))
	}
	name := "claude"
	if i < len(words) {
		name = expandHome(words[i])
		i++
	}
	for _, w := range words[i:] {
		cmd.Args = append(cmd.Args, expandHome(w))
	}
	cmd.Path, err = resolve(name)
	return cmd, err
}

func splitWords(s string) ([]string, error) {
	var words []string
	var b strings.Builder
	open := false
	quote := rune(0)
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				b.WriteRune(r)
			}
		case quote == '"':
			switch r {
			case '"':
				quote = 0
			case '\\':
				escaped = true
			default:
				b.WriteRune(r)
			}
		case r == '\\':
			escaped, open = true, true
		case r == '\'' || r == '"':
			quote, open = r, true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if open {
				words = append(words, b.String())
				b.Reset()
				open = false
			}
		default:
			b.WriteRune(r)
			open = true
		}
	}
	if quote != 0 || escaped {
		return nil, errors.New("Incomplete quote or escape in launch command")
	}
	if open {
		words = append(words, b.String())
	}
	return words, nil
}

func isAssignment(w string) bool {
	eq := strings.IndexByte(w, '=')
	if eq <= 0 {
		return false
	}
	for i, r := range w[:eq] {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func expandHome(w string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return w
	}
	switch {
	case w == "~":
		return home
	case strings.HasPrefix(w, "~/"):
		return home + w[1:]
	}
	if i := strings.Index(w, "=~/"); i >= 0 {
		return w[:i+1] + home + w[i+2:]
	}
	return w
}

// resolve finds an executable by its path, on PATH, or where installers put
// it, as an app started from the Finder has a short PATH.
func resolve(name string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) {
		if executable(name) {
			return name, nil
		}
		return "", fmt.Errorf("Executable %s not found", name)
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, dir := range []string{filepath.Join(home, ".local/bin"), "/opt/homebrew/bin", "/usr/local/bin"} {
		if p := filepath.Join(dir, name); executable(p) {
			return p, nil
		}
	}
	if name == "claude" {
		return "", errors.New("Claude Code not found. Install and sign in to the Claude Code CLI first.")
	}
	return "", fmt.Errorf("Command %s not found", name)
}

func executable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

// The command that starts Claude Code: claude by default, or one with
// options and variables of its own, as `claude --settings ~/x.json` or
// `CLAUDE_CONFIG_DIR=~/.claude-work claude`. CLAUDE_COMMAND overrides the
// saved setting.
func LoadCommand() string {
	if v := strings.TrimSpace(os.Getenv("CLAUDE_COMMAND")); v != "" {
		return v
	}
	path := appdir.File("quickgui-settings.json")
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var settings struct {
		ClaudeCommand string `json:"claudeCommand"`
	}
	_ = json.Unmarshal(data, &settings)
	return strings.TrimSpace(settings.ClaudeCommand)
}
