package projects_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"miya/internal/agent/codex/projects"
	"miya/internal/project/registry"
)

func TestTrustedProjectsComposeWithCurrentAndRetainDiscoveryOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	one, space, current := filepath.Join(home, "one"), filepath.Join(home, "space name"), filepath.Join(home, "current")
	config := fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n[projects.%q]\ntrust_level = \"trusted\"\n[projects.%q]\n", filepath.ToSlash(one), filepath.ToSlash(space), filepath.ToSlash(one))
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	got := registry.LoadProjects(current, projects.Trusted())
	var paths, names []string
	for _, p := range got {
		paths = append(paths, p.Path)
		names = append(names, p.Name)
	}
	if !reflect.DeepEqual(paths, []string{one, space, current}) || !reflect.DeepEqual(names, []string{"one", "space name", "current"}) {
		t.Fatalf("project discovery changed: %+v", got)
	}
	if got := registry.LoadProjects(one, projects.Trusted()); len(got) != 2 {
		t.Fatalf("current project duplicated: %+v", got)
	}
}
