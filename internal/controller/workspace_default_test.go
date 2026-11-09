package controller

import (
	"os"
	"path/filepath"
	"testing"

	"miya/internal/prefs"
)

// An app opened from the Dock runs in "/": that must not become the
// background service's current project.
func TestDefaultWorkspaceReplacesRoot(t *testing.T) {
	t.Setenv("MIYA_DIR", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := defaultWorkspace("/Users/x/project"); got != "/Users/x/project" {
		t.Fatalf("a real project was replaced: %q", got)
	}
	if got := defaultWorkspace("/"); got != home {
		t.Fatalf("root without a remembered project = %q, want home %q", got, home)
	}
	project := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	prefs.Save(lastWorkspaceKey, project)
	if got := defaultWorkspace("/"); got != project {
		t.Fatalf("root = %q, want the remembered project %q", got, project)
	}
	if err := os.Remove(project); err != nil {
		t.Fatal(err)
	}
	if got := defaultWorkspace("/"); got != home {
		t.Fatalf("a removed project was used: %q", got)
	}
}
