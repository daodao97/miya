package pi

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"

	"miya/internal/appdir"
)

//go:embed assets/miya-provider.ts
var extensionSource []byte

// EnsureExtension writes Miya's provider extension where pi can load it and
// returns its path. It holds no credentials.
func EnsureExtension() (string, error) {
	dir := appdir.Dir()
	if dir == "" {
		return "", os.ErrNotExist
	}
	path := filepath.Join(dir, "pi", "miya-provider.ts")
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, extensionSource) {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, extensionSource, 0o600); err != nil {
		return "", err
	}
	return path, nil
}
