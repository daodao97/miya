package cliupdate

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"miya/internal/agent/catalog"
)

func TestWindowsCodexInstallUsesOfficialInstaller(t *testing.T) {
	tools := t.TempDir()
	t.Setenv("PATH", tools)
	t.Setenv("NoDefaultCurrentDirectoryInExePath", "1")
	shell := put(t, filepath.Join(tools, "powershell.exe"), "fixture")
	for _, npmPresent := range []bool{false, true} {
		if npmPresent {
			put(t, filepath.Join(tools, "npm.cmd"), "@echo fixture")
		}
		p, err := installPlan(context.Background(), catalog.Codex, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if p.via != "self" || !strings.EqualFold(p.args[0], shell) ||
			!slices.Contains(p.env, "CODEX_NON_INTERACTIVE=1") ||
			!strings.Contains(p.shown(), "https://chatgpt.com/codex/install.ps1") {
			t.Fatalf("npm present in PATH = %t: unexpected installer %+v", npmPresent, p)
		}
		info := Check(context.Background(), catalog.Codex, "", nil)
		if !info.Install || info.Error != "" || info.Command != p.shown() {
			t.Fatalf("Codex install unavailable in settings: %+v", info)
		}
	}
}
