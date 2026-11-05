// Package appdir locates the directory where Miya keeps its service state.
package appdir

import (
	"os"
	"path/filepath"
)

// Dir keeps the service's lock, socket and log: $MIYA_DIR, or Miya in the
// user's configuration directory.
func Dir() string {
	if d := os.Getenv("MIYA_DIR"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "Miya")
}
