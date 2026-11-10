package cli

import (
	"os"
	"path/filepath"
	"strings"

	"miya/internal/processenv"
)

// GUI launches have a smaller PATH than login shells. Probe ordinary install
// locations without sourcing shell startup files or invoking a shell.
func FindBinary(name string) (string, error) {
	return processenv.LookPath(name)
}

func CommandEnv(agent, dir, binary string) []string {
	key := configEnv(agent)
	var env []string
	for _, e := range os.Environ() {
		name, _, _ := strings.Cut(e, "=")
		if name == key || name == "PATH" || (strings.HasPrefix(name, "RETTY_") || strings.HasPrefix(name, "MIYA_")) || name == "CLAUDECODE" || name == "CLAUDE_CODE_ENTRYPOINT" {
			continue
		}
		env = append(env, e)
	}
	env = append(env, key+"="+dir, "PATH="+os.Getenv("PATH"), "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1", "DISABLE_AUTOUPDATER=1")
	return processenv.WithPath(env, binary)
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
