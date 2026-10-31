//go:build !windows

package claude

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseCommand(t *testing.T) {
	home, _ := os.UserHomeDir()
	dir := t.TempDir()
	bin := filepath.Join(dir, "my claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd, err := ParseCommand(`CLAUDE_CONFIG_DIR=~/.cw A=1 '` + bin + `' --settings ~/s.json --append-system-prompt "say \"hi\"" --x=~/y a\ b`)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != bin {
		t.Fatalf("path %q", cmd.Path)
	}
	if want := []string{"CLAUDE_CONFIG_DIR=" + home + "/.cw", "A=1"}; !reflect.DeepEqual(cmd.Env, want) {
		t.Fatalf("env %q", cmd.Env)
	}
	if want := []string{"--settings", home + "/s.json", "--append-system-prompt", `say "hi"`, "--x=" + home + "/y", "a b"}; !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("args %q", cmd.Args)
	}
	for _, bad := range []string{`claude '--settings`, `claude \`, `/definitely/not/claude`} {
		if _, err := ParseCommand(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	t.Setenv("PATH", dir)
	if cmd, err := ParseCommand("  "); err == nil && cmd.Path != "" && len(cmd.Args) != 0 {
		t.Fatalf("empty command %+v", cmd)
	}
}

func TestStartPassesCommandArgsAndEnv(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "argv")
	bin := filepath.Join(dir, "fake-claude")
	script := "#!/bin/sh\nprintf '%s\\n' \"$MIYA_MARK\" \"$@\" > \"$MIYA_OUT\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	_, err := Start(context.Background(), Options{
		Binary: bin, Cwd: dir, PermissionMode: "manual",
		Args: []string{"--settings", "/tmp/s.json"},
		Env:  []string{"MIYA_OUT=" + out, "MIYA_MARK=marked"},
	}, func(Frame) {}, func(e error) { done <- e })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Minute):
		t.Fatal("fake claude did not finish")
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 4 || lines[0] != "marked" || lines[1] != "--settings" || lines[2] != "/tmp/s.json" || lines[3] != "--print" {
		t.Fatalf("argv %q", lines)
	}
}
