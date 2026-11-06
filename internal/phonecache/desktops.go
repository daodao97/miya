package phonecache

import (
	"os"
	"path/filepath"

	"miya/internal/model"
	"miya/internal/workspaces"
)

func MigrateOptions(dataDir, link string) {
	if dataDir == "" || link == "" {
		return
	}
	destination := filepath.Join(dataDir, "threads", workspaces.CacheName(link), "message-options.json")
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		return
	}
	if data, err := os.ReadFile(filepath.Join(dataDir, "message-options.json")); err == nil {
		if os.MkdirAll(filepath.Dir(destination), 0700) == nil {
			_ = os.WriteFile(destination, data, 0600)
		}
	}
}

// Links contain pairing secrets. The complete registry lives in Keychain;
// its opaque IDs, rather than links, identify controls and notifications.
type Desktop struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Link string `json:"link"`
}

// Retain the phone's in-progress composer when switching workspaces, keeping
// attachments owned by that desktop instead of deleting them on navigation.
type Session struct {
	Thread             model.Thread
	Selected, Agent    string
	Draft              string
	Images             []string
	ExpandedProject    string
	ProjectsExpanded   bool
	ThreadListExpanded map[string]bool
}

const DesktopsKey = "desktops-v1"

type SecretStore interface {
	Get(string) ([]byte, error)
	Set(string, []byte) error
	Delete(string) error
}
