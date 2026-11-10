package processenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestWithPathPreservesOverridesAndIsolation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix tool directories")
	}
	home := t.TempDir()
	env := []string{"HOME=" + home, "PATH=/old", "TOKEN=old", "PATH=/custom/bin:/usr/bin:/bin", "TOKEN=new"}
	original := slices.Clone(env)
	t.Setenv("MIYA_ENV_PARENT_ONLY", "must not leak")
	out := WithPath(env, "/launcher/bin/agent")
	var path string
	for _, entry := range out {
		if strings.HasPrefix(entry, "PATH=") {
			path = strings.TrimPrefix(entry, "PATH=")
		}
		if strings.HasPrefix(entry, "MIYA_ENV_PARENT_ONLY=") {
			t.Fatal("isolated environment inherited a parent variable")
		}
	}
	if !strings.HasPrefix(path, "/custom/bin:/usr/bin:/bin:/launcher/bin:") {
		t.Fatalf("user PATH precedence lost: %s", path)
	}
	for _, dir := range []string{filepath.Join(home, ".local/bin"), filepath.Join(home, ".cargo/bin"), filepath.Join(home, "go/bin"), "/opt/homebrew/bin", "/usr/local/bin"} {
		if !slices.Contains(filepath.SplitList(path), dir) {
			t.Errorf("missing tool directory %s", dir)
		}
	}
	if !slices.Contains(out, "TOKEN=new") || slices.Contains(out, "TOKEN=old") {
		t.Fatal("environment override was lost")
	}
	if !reflect.DeepEqual(env, original) {
		t.Fatal("input environment mutated")
	}
	if again := WithPath(out, "/launcher/bin/agent"); !reflect.DeepEqual(again, out) {
		t.Fatal("repeated preparation changed the environment")
	}
}

func TestEnvironmentWithoutPath(t *testing.T) {
	env := WithPath([]string{"MARK=kept"}, os.Args[0])
	if !slices.Contains(env, "MARK=kept") {
		t.Fatal("unrelated variable lost")
	}
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") && slices.Contains(filepath.SplitList(strings.TrimPrefix(entry, "PATH=")), filepath.Dir(os.Args[0])) {
			return
		}
	}
	t.Fatal("launcher directory missing when PATH is unset")
}

func TestChildFindsRuntimeAndToolsFromDesktopEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX env shebang")
	}
	home, launcher := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	toolDir := filepath.Join(home, ".cargo/bin")
	if err := os.MkdirAll(toolDir, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(launcher, "miya-test-runtime"): "#!/bin/sh\nexec /bin/sh \"$@\"\n",
		filepath.Join(launcher, "agent"):             "#!/usr/bin/env miya-test-runtime\nexec miya-test-tool\n",
		filepath.Join(toolDir, "miya-test-tool"):     "#!/bin/sh\nprintf 'tool found'\n",
	}
	for path, script := range files {
		if err := os.WriteFile(path, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	tool, err := LookPath("miya-test-tool")
	if err != nil || tool != filepath.Join(toolDir, "miya-test-tool") {
		t.Fatalf("discovery did not use common paths: %q %v", tool, err)
	}
	binary := filepath.Join(launcher, "agent")
	cmd := exec.Command(binary)
	cmd.Env = Environ(binary, "MARK=overridden")
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "tool found" {
		t.Fatalf("runtime/tool environment did not reach child: %q %v", out, err)
	}
}
