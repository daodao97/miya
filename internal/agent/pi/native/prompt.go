package pi

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
	"slices"

	"miya/internal/appdir"
)

//go:embed assets/miya-system.md
var systemPromptSource []byte

// systemPromptArgs replaces pi's default prompt while leaving its project
// context and skills loading intact. An explicit launch-command override wins.
func systemPromptArgs(args []string) ([]string, error) {
	if slices.Contains(args, "--system-prompt") {
		return args, nil
	}
	path, err := ensureSystemPrompt()
	if err != nil {
		return nil, err
	}
	return append(slices.Clone(args), "--system-prompt", path), nil
}

func ensureSystemPrompt() (string, error) {
	path := filepath.Join(appdir.Dir(), "pi", "miya-system.md")
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, systemPromptSource) {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	// Multiple conversations can start together; publish only a complete file.
	f, err := os.CreateTemp(filepath.Dir(path), ".miya-system-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(systemPromptSource)
	closeErr := f.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
