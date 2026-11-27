package cliupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"strings"

	"miya/internal/agent/catalog"
	"miya/internal/processenv"
)

var wslRun = func(ctx context.Context, t processenv.WSLTarget, script string, args, env []string) (string, error) {
	a := []string{processenv.WSLBinary(t.Distribution, "sh"), "-c", script, "miya"}
	out, err := run(ctx, append(a, args...), env, "")
	if err != nil && out != "" {
		err = fmt.Errorf("%s: %w", out, err)
	}
	return out, err
}

func isMissingWSL(err error) bool {
	var e *exec.ExitError
	return errors.As(err, &e) && e.ExitCode() == 127
}
func wslResolve(ctx context.Context, t processenv.WSLTarget, env []string) (string, error) {
	return wslRun(ctx, t, `p=$(command -v -- "$1") || exit 127
case "$p" in /mnt/*|*.exe|*.cmd|*.bat) exit 127;; esac
readlink -f -- "$p"`, []string{t.Binary}, env)
}
func checkWSL(ctx context.Context, id, bin string, env []string) Info {
	t, _ := processenv.WSL(bin)
	real, err := wslResolve(ctx, t, env)
	if isMissingWSL(err) {
		if d, ok := catalog.Lookup(id); !ok || t.Binary != d.Command {
			return Info{Path: bin, Error: tr("Install the CLI configured in the launch command, then check again")}
		}
		return missingInfo(ctx, id, bin, env)
	}
	info := Info{Path: bin}
	if err != nil {
		info.Error = err.Error()
		return info
	}
	info.Version, err = version(ctx, processenv.WSLBinary(t.Distribution, real), env)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	p := wslInstalledPlan(ctx, id, t, real, env)
	if p == nil {
		return info
	}
	info.Via, info.Command = p.via, p.shown()
	info.Latest, err = p.latest(ctx)
	if err != nil {
		info.Error = err.Error()
	} else if info.Latest == "" || parseVersion(info.Latest) != info.Latest {
		info.Latest = ""
		info.Error = tr("No release version found")
	}
	info.Update = newer(info.Latest, info.Version)
	return info
}
func upgradePlan(ctx context.Context, id, bin string, env []string) *plan {
	if t, ok := processenv.WSL(bin); ok {
		real, err := wslResolve(ctx, t, env)
		if err != nil {
			return nil
		}
		return wslInstalledPlan(ctx, id, t, real, env)
	}
	return installedPlan(id, bin, env)
}
func wslInstalledPlan(ctx context.Context, id string, t processenv.WSLTarget, real string, env []string) *plan {
	s, ok := specs[id]
	if !ok {
		return nil
	}
	wrap := func(binary string) string { return processenv.WSLBinary(t.Distribution, binary) }
	if id == catalog.Claude && strings.Contains(real, "/.local/share/claude/versions/") || id == catalog.Codex && strings.Contains(real, "/.codex/packages/standalone/") {
		return &plan{via: "self", pkg: s.packages[0], args: []string{wrap(t.Binary), "update"}, env: env}
	}
	for _, pkg := range s.packages {
		mark := "/node_modules/" + pkg + "/"
		i := strings.Index(real, mark)
		if i < 0 {
			continue
		}
		modules := real[:i] + "/node_modules"
		b, err := wslRun(ctx, t, `cat -- "$1"`, []string{modules + "/" + pkg + "/package.json"}, env)
		var m struct{ Name string }
		if err != nil || json.Unmarshal([]byte(b), &m) != nil || m.Name != pkg {
			return nil
		}
		if strings.Contains(real, "/.volta/") || strings.Contains(real, "/.yarn/") || strings.Contains(real, "/yarn/") {
			return nil
		}
		for _, via := range []string{"bun", "pnpm"} {
			if via == "bun" && strings.Contains(real, "/.bun/") || via == "pnpm" && (strings.Contains(real, "/pnpm/") || strings.Contains(real, "/.pnpm/")) {
				manager, err := wslRun(ctx, t, `command -v -- "$1"`, []string{via}, env)
				if err != nil {
					return nil
				}
				return &plan{via: via, pkg: pkg, args: []string{wrap(manager), "add", "-g", pkg + "@latest"}, env: env}
			}
		}
		prefix := path.Dir(modules)
		if path.Base(prefix) != "lib" {
			return nil
		}
		prefix = path.Dir(prefix)
		npm, err := wslRun(ctx, t, `command -v npm`, nil, env)
		if err != nil {
			return nil
		}
		return &plan{via: "npm", pkg: pkg, args: []string{wrap(npm), "install", "-g", "--prefix", prefix, pkg + "@latest"}, env: env}
	}
	return nil
}
