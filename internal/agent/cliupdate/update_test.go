package cliupdate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func put(t *testing.T, path, data string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
func link(t *testing.T, target, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	return path
}
func unix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
}
func fakeNPM(t *testing.T, home, pkg string) (string, string, string) {
	t.Helper()
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	home, _ = filepath.EvalSymlinks(home)
	prefix := filepath.Join(home, "node with spaces")
	target := put(t, filepath.Join(prefix, "lib/node_modules", pkg, "bin/agent"), "#!/bin/sh\necho 'codex-cli 1.2.3'\n")
	put(t, filepath.Join(prefix, "lib/node_modules", pkg, "package.json"), fmt.Sprintf(`{"name":%q}`, pkg))
	bin := link(t, target, filepath.Join(prefix, "bin/codex"))
	npm := put(t, filepath.Join(prefix, "bin/npm"), "#!/bin/sh\nexit 0\n")
	return bin, npm, prefix
}

func TestInstallationOwnership(t *testing.T) {
	unix(t)
	bin, npm, prefix := fakeNPM(t, t.TempDir(), "@openai/codex")
	p := installedPlan("codex", bin, nil)
	want := []string{npm, "install", "-g", "--prefix", prefix, "@openai/codex@latest"}
	if p == nil || p.via != "npm" || !reflect.DeepEqual(p.args, want) || p.prefix != filepath.Dir(npm) {
		t.Fatalf("npm plan: %+v", p)
	}
	if !strings.Contains(p.shown(), fmt.Sprintf("%q", prefix)) {
		t.Fatal(p.shown())
	}
	if installedPlan("pi", bin, nil) != nil {
		t.Fatal("unrelated package was accepted")
	}
	unknown := put(t, filepath.Join(t.TempDir(), "codex"), "#!/bin/sh\necho 1.2.3\n")
	if installedPlan("codex", unknown, nil) != nil {
		t.Fatal("guessed installer for standalone binary")
	}
	// A local project dependency must not be rewritten as a global package.
	local := put(t, filepath.Join(t.TempDir(), "node_modules/@openai/codex/bin/codex"), "#!/bin/sh\n")
	put(t, filepath.Join(filepath.Dir(filepath.Dir(local)), "package.json"), `{"name":"@openai/codex"}`)
	if installedPlan("codex", local, nil) != nil {
		t.Fatal("local dependency accepted as global")
	}
}

func TestBrewAndNativeOwnership(t *testing.T) {
	unix(t)
	for _, kind := range []string{"Cellar", "Caskroom"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			root, _ = filepath.EvalSymlinks(root)
			brew := put(t, filepath.Join(root, "bin/brew"), "#!/bin/sh\n")
			bin := put(t, filepath.Join(root, kind, "codex/1.2.3/bin/codex"), "#!/bin/sh\n")
			p := installedPlan("codex", bin, nil)
			if p == nil || p.args[0] != brew || p.via != "brew" || p.cask != (kind == "Caskroom") {
				t.Fatalf("brew: %+v", p)
			}
		})
	}
	for _, tc := range []struct{ id, path string }{{"claude-code", ".local/share/claude/versions/1.2.3"}, {"codex", ".codex/packages/standalone/releases/1.2.3/codex"}} {
		bin := put(t, filepath.Join(t.TempDir(), tc.path), "#!/bin/sh\n")
		p := installedPlan(tc.id, bin, nil)
		if p == nil || p.via != "self" || !reflect.DeepEqual(p.args, []string{bin, "update"}) {
			t.Fatalf("native: %+v", p)
		}
	}
	// npm installed under Homebrew's node remains npm's.
	bin, _, _ := fakeNPM(t, filepath.Join(t.TempDir(), "Cellar/node/22"), "@openai/codex")
	if p := installedPlan("codex", bin, nil); p == nil || p.via != "npm" {
		t.Fatalf("brew node: %+v", p)
	}
}

func TestVersionOrdering(t *testing.T) {
	for _, tc := range []struct {
		a, b  string
		newer bool
	}{{"1.10.0", "1.9.0", true}, {"2.0.0", "2.0.0-beta.1", true}, {"2.0.0-beta.11", "2.0.0-beta.2", true}, {"2.0.0-beta.1", "2.0.0", false}, {"1.2.3", "1.2.3", false}, {"", "1.2.3", false}} {
		if got := newer(tc.a, tc.b); got != tc.newer {
			t.Errorf("newer(%q,%q)=%v", tc.a, tc.b, got)
		}
	}
	if got := parseVersion("claude 2.1.23 (Claude Code)"); got != "2.1.23" {
		t.Fatal(got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func serveVersion(t *testing.T) {
	t.Helper()
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "127.0.0.1:1" {
			return nil, errors.New("offline")
		}
		if r.URL.Path != "/@openai/codex/latest" {
			t.Errorf("wrong package: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"version":"1.3.0"}`)), Header: make(http.Header)}, nil
	})}
	oldRegistry := registry
	t.Cleanup(func() { http.DefaultClient = old; registry = oldRegistry })
}
func TestCheckAndUpgradeVerifyExecutable(t *testing.T) {
	unix(t)
	serveVersion(t)
	bin, npm, prefix := fakeNPM(t, t.TempDir(), "@openai/codex")
	ctx := context.Background()
	info := Check(ctx, "codex", bin, nil)
	if info.Version != "1.2.3" || info.Latest != "1.3.0" || !info.Update || info.Error != "" {
		t.Fatalf("check: %+v", info)
	}
	// Exit zero without changing the executable is never called success.
	info = Upgrade(ctx, "codex", bin, nil)
	if info.Error == "" || info.Previous != "1.2.3" {
		t.Fatalf("unverified success: %+v", info)
	}
	target, _ := filepath.EvalSymlinks(bin)
	// The matching node, absent from the parent PATH, must run the updater.
	put(t, filepath.Join(prefix, "bin/miya-test-node"), "#!/bin/sh\nexec /bin/sh \"$@\"\n")
	put(t, npm, "#!/usr/bin/env miya-test-node\n[ \"$1\" = install ] && [ \"$2\" = -g ] && [ \"$3\" = --prefix ] && [ \"$4\" = \""+prefix+"\" ] && [ \"$5\" = @openai/codex@latest ] || exit 9\nprintf '#!/bin/sh\\necho 1.3.0\\n' > '"+target+"'\n")
	info = Upgrade(ctx, "codex", bin, nil)
	if info.Version != "1.3.0" || info.Previous != "1.2.3" || info.Update || info.Error != "" {
		t.Fatalf("upgrade: %+v", info)
	}
}
func TestFailuresPreserveInstalledVersion(t *testing.T) {
	unix(t)
	serveVersion(t)
	bin, npm, _ := fakeNPM(t, t.TempDir(), "@openai/codex")
	put(t, npm, "#!/bin/sh\necho 'permission denied' >&2\nexit 1\n")
	info := Upgrade(context.Background(), "codex", bin, nil)
	if info.Version != "1.2.3" || !strings.Contains(info.Error, "permission denied") || !info.Update {
		t.Fatalf("failure: %+v", info)
	}
	registry = "http://127.0.0.1:1/"
	info = Check(context.Background(), "codex", bin, nil)
	if info.Version != "1.2.3" || info.Update || info.Error == "" {
		t.Fatalf("offline: %+v", info)
	}
}

func TestOtherPackageManagersAndLegacyPi(t *testing.T) {
	unix(t)
	for _, tc := range []struct{ folder, via string }{{".bun/install/global", "bun"}, {".local/share/pnpm/global/5", "pnpm"}} {
		t.Run(tc.via, func(t *testing.T) {
			root := t.TempDir()
			pkg := "@mariozechner/pi-coding-agent"
			target := put(t, filepath.Join(root, tc.folder, "node_modules", pkg, "dist/cli.js"), "#!/bin/sh\necho 1.0.0\n")
			put(t, filepath.Join(root, tc.folder, "node_modules", pkg, "package.json"), fmt.Sprintf(`{"name":%q}`, pkg))
			bin := link(t, target, filepath.Join(root, "bin/pi"))
			m := put(t, filepath.Join(root, "bin", tc.via), "#!/bin/sh\n")
			p := installedPlan("pi", bin, nil)
			if p == nil || p.via != tc.via || !reflect.DeepEqual(p.args, []string{m, "add", "-g", pkg + "@latest"}) {
				t.Fatalf("manager: %+v", p)
			}
		})
	}
}
func TestHomebrewLatestUsesItsOwnRelease(t *testing.T) {
	unix(t)
	root := t.TempDir()
	bin := put(t, filepath.Join(root, "Caskroom/codex/1.2.3/codex"), "#!/bin/sh\necho 1.2.3\n")
	put(t, filepath.Join(root, "bin/brew"), "#!/bin/sh\n[ \"$1\" = info ] && [ \"$2\" = --json=v2 ] && [ \"$3\" = --cask ] && [ \"$4\" = codex ] || exit 9\necho '{\"casks\":[{\"version\":\"1.3.0,456\"}]}'\n")
	info := Check(context.Background(), "codex", bin, nil)
	if info.Version != "1.2.3" || info.Latest != "1.3.0" || info.Via != "brew" || !info.Update || info.Error != "" {
		t.Fatalf("brew release: %+v", info)
	}
}
