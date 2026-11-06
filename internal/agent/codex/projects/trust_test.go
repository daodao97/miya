package projects_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"miya/internal/agent/codex/projects"
)

func TestProjectRegistrationPreservesOtherCodexSettingsAndDoesNotDuplicate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "config.toml")
	const original = "model = \"fixture\"\n"
	if err := os.WriteFile(config, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "project with spaces")
	projects.Trust(path)
	projects.Trust(path)
	data, err := os.ReadFile(config)
	if err != nil || !strings.HasPrefix(string(data), original) || strings.Count(string(data), "trust_level = \"trusted\"") != 1 {
		t.Fatalf("registration lost settings or duplicated trust: %s %v", data, err)
	}
	projects.Forget(path)
	data, err = os.ReadFile(config)
	if err != nil || string(data) != original+"\n" {
		t.Fatalf("project removal changed other settings: %s %v", data, err)
	}
}
