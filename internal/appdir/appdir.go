// Package appdir locates the directory where Miya keeps its service state.
package appdir

import (
	"os"
	"path/filepath"
)

// Dir keeps the service's lock, socket and log: $MIYA_ISOLATED_DIR for an
// isolated instance, else $MIYA_DIR, else Miya in the user's configuration
// directory.
func Dir() string {
	if d := os.Getenv("MIYA_ISOLATED_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("MIYA_DIR"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "Miya")
}

// Isolated reports whether MIYA_ISOLATED_DIR gives this instance a
// directory of its own, as a development build run beside the installed app
// does. An isolated instance keeps all of Miya's state in Dir and leaves the
// user's login items alone; agents' own files, such as ~/.codex/config.toml,
// stay shared. MIYA_DIR alone does not isolate: a server keeps its service
// state there and its settings and history where they always were.
func Isolated() bool { return os.Getenv("MIYA_ISOLATED_DIR") != "" }

// File is where Miya keeps one of its files that predate Dir:
// ~/.codex/name for the installed app, so existing state keeps working, or
// Dir()/name for an isolated instance. It is "" without a home directory.
func File(name string) string {
	if Isolated() {
		return filepath.Join(Dir(), name)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", name)
}
