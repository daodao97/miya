package settings

import (
	"encoding/json"
	"os"
	"path/filepath"

	"miya/internal/prefs"
)

func SaveCommand(command string) {
	path := prefs.SettingsPath()
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

func LoadCommand() string { return prefs.Load("claudeCommand") }
