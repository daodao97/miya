package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GUI launches have a smaller PATH than login shells. Probe ordinary install
// locations without sourcing shell startup files or invoking a shell.
func FindBinary(name string) (string, error) {
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	for _, dir := range installDirs() {
		path := filepath.Join(dir, name)
		if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0111 != 0 {
			return path, nil
		}
	}
	return "", os.ErrNotExist
}

func installDirs() []string {
	home, _ := os.UserHomeDir()
	return []string{filepath.Join(home, ".local", "bin"), filepath.Join(home, ".npm-global", "bin"), "/opt/homebrew/bin", "/usr/local/bin"}
}

// Finding an npm launcher by absolute path is insufficient: its env shebang
// still needs node. Give it the same install locations we searched for it.
func executionPath() string {
	var dirs []string
	seen := make(map[string]bool)
	for _, dir := range append(filepath.SplitList(os.Getenv("PATH")), installDirs()...) {
		if filepath.IsAbs(dir) && !seen[dir] {
			dirs = append(dirs, dir)
			seen[dir] = true
		}
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}

func CommandEnv(agent, dir string) []string {
	key := configEnv(agent)
	var env []string
	for _, e := range os.Environ() {
		name, _, _ := strings.Cut(e, "=")
		if name == key || name == "PATH" || (strings.HasPrefix(name, "RETTY_") || strings.HasPrefix(name, "MIYA_")) || name == "CLAUDECODE" || name == "CLAUDE_CODE_ENTRYPOINT" {
			continue
		}
		env = append(env, e)
	}
	return append(env, key+"="+dir, "PATH="+executionPath(), "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1", "DISABLE_AUTOUPDATER=1")
}

// Use the CLI's configured account directory without reading credentials.
func configEnv(agent string) string {
	if agent == "codex" {
		return "CODEX_HOME"
	}
	if agent == "claude" {
		return "CLAUDE_CONFIG_DIR"
	}
	return ""
}
func ConfigDir(agent string) (string, error) {
	if dir := os.Getenv(configEnv(agent)); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "."+agent), nil
}
