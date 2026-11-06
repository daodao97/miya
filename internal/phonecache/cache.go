// Package phonecache keeps what the phone app saves between launches: the
// state and conversations of each paired desktop, and the desktops themselves.
package phonecache

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"miya/internal/phoneapi"
)

type SavedState struct {
	Host  string         `json:"host"`
	State phoneapi.State `json:"state"`
}

// pruneCache keeps the keep most recently written conversations.
func Prune(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type file struct {
		name string
		at   time.Time
	}
	var files []file
	for _, e := range entries {
		if e.Name() == StateFile || !strings.HasSuffix(e.Name(), ".json.gz") {
			continue
		}
		if info, err := e.Info(); err == nil {
			files = append(files, file{e.Name(), info.ModTime()})
		}
	}
	if len(files) <= keep {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].at.Before(files[j].at) })
	for _, f := range files[:len(files)-keep] {
		os.Remove(filepath.Join(dir, f.name))
	}
}

// The phone keeps what it last saw of each desktop on disk, so it shows
// the conversations at once after a relaunch and asks the desktop only for
// what changed. Each desktop has a directory of its own, named by its link.
const (
	PhoneDiskThreads = 50
	CacheFlushDelay  = 800 * time.Millisecond
	StateKey         = "\x00state"
	StateFile        = "state.json.gz"
)
