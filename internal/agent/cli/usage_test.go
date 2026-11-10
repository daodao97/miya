package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDesktopPathSupportsEnvShebangLaunchers(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX env shebang")
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("PATH", "/usr/bin:/bin")
	dir := filepath.Join(root, ".local", "bin")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{"usage-node": "#!/bin/sh\nprintf 'runtime found'\n", "codex": "#!/usr/bin/env usage-node\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	binary, err := FindBinary("codex")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary)
	cmd.Env = CommandEnv("codex", root, binary)
	out, err := cmd.Output()
	if err != nil || string(out) != "runtime found" {
		t.Fatalf("GUI launcher lost runtime: %q %v", out, err)
	}
}
