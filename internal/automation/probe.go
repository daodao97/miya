package automation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// fileSnapshot is what a file trigger last saw: each entry's size and
// modification time.
type fileSnapshot map[string]string

// fileEntryLimit bounds the entries of a watched directory.
const fileEntryLimit = 5000

// snapshotPath reads a file, or the entries of a directory, without going
// into subdirectories. A missing path is an empty snapshot.
func snapshotPath(path string) (fileSnapshot, error) {
	snap := fileSnapshot{}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return snap, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		snap[filepath.Base(path)] = entryStamp(info)
		return snap, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	for i, e := range entries {
		if i >= fileEntryLimit {
			break
		}
		if info, err := e.Info(); err == nil {
			snap[e.Name()] = entryStamp(info)
		}
	}
	return snap, nil
}

func entryStamp(info os.FileInfo) string {
	return strconv.FormatInt(info.Size(), 10) + "@" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
}

// fingerprint identifies a snapshot, to compare with one from before the
// service restarted.
func (s fileSnapshot) fingerprint() string {
	names := make([]string, 0, len(s))
	for n := range s {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		h.Write([]byte(n + "\x00" + s[n] + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// fileChanges lists what changed from before to after.
func fileChanges(before, after fileSnapshot) (added, removed, modified []string) {
	for n, stamp := range after {
		if old, ok := before[n]; !ok {
			added = append(added, n)
		} else if old != stamp {
			modified = append(modified, n)
		}
	}
	for n := range before {
		if _, ok := after[n]; !ok {
			removed = append(removed, n)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(modified)
	return
}

// describeFileChanges says what changed, naming at most a few entries.
func describeFileChanges(added, removed, modified []string) string {
	var parts []string
	list := func(label string, names []string) {
		if len(names) == 0 {
			return
		}
		shown := names
		if len(shown) > 20 {
			shown = shown[:20]
		}
		line := fmt.Sprintf("%s (%d): %s", label, len(names), strings.Join(shown, ", "))
		if len(names) > len(shown) {
			line += ", …"
		}
		parts = append(parts, line)
	}
	list("Added", added)
	list("Removed", removed)
	list("Modified", modified)
	return strings.Join(parts, "\n")
}
