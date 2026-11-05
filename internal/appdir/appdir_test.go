package appdir

import (
	"path/filepath"
	"testing"
)

func TestFileStaysInCodexForTheInstalledApp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MIYA_ISOLATED_DIR", "")
	t.Setenv("MIYA_DIR", "")
	if Isolated() {
		t.Fatal("installed app reported as isolated")
	}
	if got, want := File("quickgui-settings.json"), filepath.Join(home, ".codex", "quickgui-settings.json"); got != want {
		t.Fatalf("File = %q, want %q", got, want)
	}
}

// A server's MIYA_DIR moves its service state only: its settings and history
// stay where docs/server.md promises.
func TestMIYADIRAloneDoesNotIsolate(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MIYA_ISOLATED_DIR", "")
	t.Setenv("MIYA_DIR", data)
	if Isolated() || Dir() != data {
		t.Fatalf("Isolated = %v, Dir = %q", Isolated(), Dir())
	}
	if got, want := File("quickgui-settings.json"), filepath.Join(home, ".codex", "quickgui-settings.json"); got != want {
		t.Fatalf("File = %q, want %q", got, want)
	}
}

func TestIsolatedDirHoldsAllState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MIYA_DIR", t.TempDir())
	t.Setenv("MIYA_ISOLATED_DIR", dir)
	if !Isolated() || Dir() != dir {
		t.Fatalf("Isolated = %v, Dir = %q", Isolated(), Dir())
	}
	if got, want := File("quickgui-settings.json"), filepath.Join(dir, "quickgui-settings.json"); got != want {
		t.Fatalf("File = %q, want %q", got, want)
	}
}
