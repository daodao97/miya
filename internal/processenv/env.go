// Package processenv supplies the tool PATH shared by child processes started
// from a desktop app or background service with a minimal environment.
package processenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Environ inherits the process environment, applies overrides, and adds tool
// locations. It does not read shell startup files or change the parent process.
func Environ(binary string, overrides ...string) []string {
	return WithPath(append(os.Environ(), overrides...), binary)
}

// WithPath preserves the supplied environment and PATH precedence. Missing
// locations are appended: the launcher's directory (for env shebang runtimes),
// then common user and system tool directories. Duplicate variables use their
// last value, just as os/exec does. An isolated environment stays isolated.
func WithPath(env []string, binary string) []string {
	out := make([]string, 0, len(env)+1)
	indices := make(map[string]int)
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			out = append(out, entry)
			continue
		}
		key = envKey(key)
		if i, ok := indices[key]; ok {
			out[i] = entry
		} else {
			indices[key] = len(out)
			out = append(out, entry)
		}
	}
	var path string
	if i, ok := indices[envKey("PATH")]; ok {
		_, path, _ = strings.Cut(out[i], "=")
	}
	home, _ := os.UserHomeDir()
	homeKey := "HOME"
	if runtime.GOOS == "windows" {
		homeKey = "USERPROFILE"
	}
	if i, ok := indices[envKey(homeKey)]; ok {
		_, home, _ = strings.Cut(out[i], "=")
	}
	dirs := filepath.SplitList(path)
	seen := make(map[string]bool)
	for _, dir := range dirs {
		seen[envKey(dir)] = true
	}
	add := func(dir string) {
		if filepath.IsAbs(dir) && !seen[envKey(dir)] {
			dirs = append(dirs, dir)
			seen[envKey(dir)] = true
		}
	}
	add(filepath.Dir(binary))
	for _, dir := range installDirs(home) {
		add(dir)
	}
	entry := "PATH=" + strings.Join(dirs, string(os.PathListSeparator))
	if i, ok := indices[envKey("PATH")]; ok {
		out[i] = entry
	} else {
		out = append(out, entry)
	}
	return out
}

// LookPath searches the same tool locations supplied to child processes.
func LookPath(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err == nil || strings.ContainsAny(name, `/\`) {
		return path, err
	}
	for _, entry := range Environ("") {
		if !strings.HasPrefix(entry, "PATH=") {
			continue
		}
		for _, dir := range filepath.SplitList(strings.TrimPrefix(entry, "PATH=")) {
			if !filepath.IsAbs(dir) {
				continue
			}
			if path, lookupErr := exec.LookPath(filepath.Join(dir, name)); lookupErr == nil {
				return path, nil
			}
		}
	}
	return "", err
}

func installDirs(home string) []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	var dirs []string
	if filepath.IsAbs(home) {
		for _, dir := range []string{".local/bin", ".npm-global/bin", ".bun/bin", ".volta/bin", ".cargo/bin", "go/bin"} {
			dirs = append(dirs, filepath.Join(home, dir))
		}
	}
	return append(dirs, "/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin", "/usr/local/sbin")
}

func envKey(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}
