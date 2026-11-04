package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplicationPathsScanOnlyLauncherFolders(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"Apps/Notes.app", "Apps/Tools/Terminal.app", "Apps/Notes.app/Contents/Helper.app", "Apps/Tools/Deep/Hidden.app", "Apps/.private/Hidden.app", "Apps/.Secret.app", "User/Notes.app", "Core/Finder.app", "Core/Dock.app"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "Apps/File.app"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	dirs := []string{filepath.Join(root, "Apps"), filepath.Join(root, "User"), filepath.Join(root, "missing"), filepath.Join(root, "Apps")}
	finder := filepath.Join(root, "Core/Finder.app")
	apps := applicationPaths(dirs, finder)
	if len(apps) != 4 {
		t.Fatalf("unexpected apps: %+v", apps)
	}
	for i, name := range []string{"Finder", "Notes", "Notes", "Terminal"} {
		if apps[i].Name != name {
			t.Fatal(apps)
		}
	}
	for _, id := range []string{"https://example.com", "-a", "Notes", filepath.Join(root, "Core/Dock.app"), filepath.Join(root, "Apps/Tools/Deep/Hidden.app"), filepath.Join(root, "Apps/Notes.app") + "/../Notes.app"} {
		if listedApplication(id, apps) {
			t.Fatalf("accepted unlisted app %q", id)
		}
	}
	if !listedApplication(finder, apps) {
		t.Fatal("Finder cannot launch")
	}
	if err := os.RemoveAll(finder); err != nil {
		t.Fatal(err)
	}
	if listedApplication(finder, applicationPaths(dirs, finder)) {
		t.Fatal("deleted app still launchable")
	}
}
