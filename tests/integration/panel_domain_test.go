//go:build !mygo_ui

package integration

import (
	"miya/internal/uiruntime"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// syncPanel runs the panel's disk and git work in place, so a frame after a
// click shows its result.
func syncPanel(t *testing.T) {
	t.Helper()
	old := uiruntime.GoAsync
	uiruntime.GoAsync = func(f func()) { f() }
	t.Cleanup(func() { uiruntime.GoAsync = old })
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
