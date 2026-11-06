package projects

import (
	"os"
	"path/filepath"
	"regexp"
)

func Trusted() []string {
	var paths []string
	home, err := os.UserHomeDir()
	if err == nil {
		if data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml")); err == nil {
			re := regexp.MustCompile(`(?m)^\[projects\."([^"]+)"\]`)
			for _, match := range re.FindAllStringSubmatch(string(data), -1) {
				if len(match) > 1 {
					paths = append(paths, match[1])
				}
			}
		}
	}

	return paths
}
