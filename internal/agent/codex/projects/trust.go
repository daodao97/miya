package projects

import (
	"os"
	"path/filepath"
	"strings"
)

func Trust(path string) {
	if home, err := os.UserHomeDir(); err == nil {
		configPath := filepath.Join(home, ".codex", "config.toml")
		if data, err := os.ReadFile(configPath); err == nil {
			escaped := strings.ReplaceAll(path, `"`, `\"`)
			section := "\n[projects.\"" + escaped + "\"]\ntrust_level = \"trusted\"\n"
			if !strings.Contains(string(data), "[projects.\""+escaped+"\"]") {
				_ = os.WriteFile(configPath, append(data, []byte(section)...), 0600)
			}
		}
	}

}
func Forget(path string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	configPath := filepath.Join(home, ".codex", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}
	escaped := strings.ReplaceAll(path, `"`, `\"`)
	block := "\n[projects.\"" + escaped + "\"]\ntrust_level = \"trusted\"\n"
	updated := strings.Replace(string(data), block, "\n", 1)
	if updated != string(data) {
		_ = os.WriteFile(configPath, []byte(updated), 0600)
	}

}
