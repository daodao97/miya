package control

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// The pointer mode is a phone-local preference; it is never sent to the Mac.
var desktopPrefsMu sync.Mutex

type desktopPrefs struct {
	PointerMode string `json:"pointerMode"`
}

func validPointerMode(mode string) bool { return mode == "trackpad" || mode == "touch" }

func ReadPrefs(path string) desktopPrefs {
	prefs := desktopPrefs{PointerMode: "trackpad"}
	if path == "" {
		return prefs
	}
	desktopPrefsMu.Lock()
	data, err := os.ReadFile(path)
	desktopPrefsMu.Unlock()
	var saved desktopPrefs
	if err == nil && json.Unmarshal(data, &saved) == nil && validPointerMode(saved.PointerMode) {
		prefs.PointerMode = saved.PointerMode
	}
	return prefs
}

func WritePrefs(path, mode string) error {
	if path == "" || !validPointerMode(mode) {
		return nil
	}
	data, _ := json.Marshal(desktopPrefs{PointerMode: mode})
	desktopPrefsMu.Lock()
	defer desktopPrefsMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
