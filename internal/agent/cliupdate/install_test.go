package cliupdate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"miya/internal/processenv"
)

func isolateDiscovery(t *testing.T) {
	t.Helper()
	old := findInstalled
	t.Cleanup(func() { findInstalled = old })
	findInstalled = func(name string) (string, error) {
		p := filepath.Join(processenv.ManagedBinDir(), name)
		if _, err := os.Stat(p); err != nil {
			return "", err
		}
		return p, nil
	}
}
func TestInstallUsesOwnedPrefixAndVerifiesBinary(t *testing.T) {
	unix(t)
	isolateDiscovery(t)
	serveVersion(t)
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	tools := t.TempDir()
	t.Setenv("PATH", tools)
	prefix := filepath.Dir(processenv.ManagedBinDir())
	// The fixture models npm's global layout without downloading or installing
	// any real package, and fails if the selected prefix/package is wrong.
	script := fmt.Sprintf(`#!/bin/sh
[ "$1" = install ] && [ "$2" = -g ] && [ "$3" = --prefix ] && [ "$4" = '%s' ] && [ "$5" = @openai/codex@latest ] || exit 9
/bin/mkdir -p "$4/bin" "$4/lib/node_modules/@openai/codex/bin"
printf '{"name":"@openai/codex"}' > "$4/lib/node_modules/@openai/codex/package.json"
printf '#!/bin/sh\necho 1.3.0\n' > "$4/lib/node_modules/@openai/codex/bin/codex"
/bin/chmod 700 "$4/lib/node_modules/@openai/codex/bin/codex"
/bin/ln -s "$4/lib/node_modules/@openai/codex/bin/codex" "$4/bin/codex"
`, prefix)
	put(t, filepath.Join(tools, "npm"), script)
	info := Check(context.Background(), "codex", "", nil)
	if !info.Install || info.Command == "" || info.Error != "" {
		t.Fatalf("not installable: %+v", info)
	}
	info = Upgrade(context.Background(), "codex", "", nil)
	if info.Install || info.Version != "1.3.0" || info.Error != "" || info.Path != filepath.Join(prefix, "bin/codex") {
		t.Fatalf("install not verified: %+v", info)
	}
	// A second click must discover the existing CLI without running an installer.
	put(t, filepath.Join(tools, "npm"), "#!/bin/sh\nexit 99\n")
	again := install(context.Background(), "codex", "", nil)
	if again.Version != "1.3.0" || again.Error != "" {
		t.Fatalf("reinstalled existing CLI: %+v", again)
	}
}
func TestInstallZeroExitWithoutBinaryIsFailure(t *testing.T) {
	unix(t)
	isolateDiscovery(t)
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	tools := t.TempDir()
	t.Setenv("PATH", tools)
	put(t, filepath.Join(tools, "npm"), "#!/bin/sh\nexit 0\n")
	info := install(context.Background(), "pi", "", nil)
	if !info.Install || info.Version != "" || info.Error == "" {
		t.Fatalf("false success: %+v", info)
	}
}
func TestWSLPlansStayInSelectedDistribution(t *testing.T) {
	old := wslRun
	t.Cleanup(func() { wslRun = old })
	wslRun = func(_ context.Context, tgt processenv.WSLTarget, script string, args, env []string) (string, error) {
		if tgt.Distribution != "Ubuntu test" {
			t.Fatalf("wrong distribution: %+v", tgt)
		}
		if strings.Contains(script, "cat --") {
			return `{"name":"@openai/codex"}`, nil
		}
		if strings.Contains(script, "HOME/") {
			return "/home/me/node/bin/npm\n/home/me/.local/share/miya/agents/npm", nil
		}
		return "/home/me/node/bin/npm", nil
	}
	bin := processenv.WSLBinary("Ubuntu test", "codex")
	p, err := installPlan(context.Background(), "codex", bin, nil)
	if err != nil {
		t.Fatal(err)
	}
	target, ok := processenv.WSL(p.args[0])
	if !ok || target.Distribution != "Ubuntu test" || target.Binary != "/home/me/node/bin/npm" || p.args[4] != "/home/me/.local/share/miya/agents/npm" {
		t.Fatalf("host installer leaked into WSL: %+v", p)
	}
	p = wslInstalledPlan(context.Background(), "codex", processenv.WSLTarget{Distribution: "Ubuntu test", Binary: "codex"}, "/home/me/node/lib/node_modules/@openai/codex/bin/codex.js", nil)
	if p == nil || p.via != "npm" || p.args[4] != "/home/me/node" || !processenv.IsWSL(p.args[0]) {
		t.Fatalf("wrong upgrade environment: %+v", p)
	}
}
