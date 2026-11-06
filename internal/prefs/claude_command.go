package prefs

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func SaveClaudeCommand(command string) {
	path := SettingsPath()
	if path == "" {
		return
	}
	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	if command == "" {
		delete(settings, "claudeCommand")
	} else {
		settings["claudeCommand"] = command
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, data, 0o600)
}
