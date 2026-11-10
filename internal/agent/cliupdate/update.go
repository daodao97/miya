// Package cliupdate checks and updates the installed agents through their
// original installer. Unknown installations are never replaced by a global install.
package cliupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"miya/internal/agent/catalog"
	"miya/internal/processenv"
	"miya/locales"
)

func tr(s string) string { return locales.T(s) }

type Info struct {
	Path     string `json:"path,omitempty"`
	Version  string `json:"version,omitempty"`
	Latest   string `json:"latest,omitempty"`
	Via      string `json:"via,omitempty"`
	Command  string `json:"command,omitempty"`
	Update   bool   `json:"update,omitempty"`
	Checking bool   `json:"checking,omitempty"`
	Updating bool   `json:"updating,omitempty"`
	Error    string `json:"error,omitempty"`
	Previous string `json:"previous,omitempty"`
}

type spec struct {
	packages []string
	brew     string
}

var specs = map[string]spec{
	catalog.Codex:  {[]string{"@openai/codex"}, "codex"},
	catalog.Claude: {[]string{"@anthropic-ai/claude-code"}, "claude-code"},
	catalog.Pi:     {[]string{"@earendil-works/pi-coding-agent", "@mariozechner/pi-coding-agent"}, "pi-coding-agent"},
}

type plan struct {
	via, pkg, prefix string
	cask             bool
	args             []string
	env              []string
}

func executable(path string) bool { st, err := os.Stat(path); return err == nil && !st.IsDir() }
func manager(name, dir string) string {
	if runtime.GOOS == "windows" && name != "bun" {
		name += ".cmd"
	}
	if p := filepath.Join(dir, name); executable(p) {
		return p
	}
	p, _ := processenv.LookPath(name)
	return p
}

// installedPlan follows symlinks before identifying the installer, including
// npm under Homebrew's node, which belongs to npm rather than to Homebrew.
func installedPlan(id, bin string, env []string) *plan {
	s, ok := specs[id]
	if !ok || bin == "" {
		return nil
	}
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return nil
	}
	slash := filepath.ToSlash(real)
	makePlan := func(via, pkg string, args ...string) *plan { return &plan{via: via, pkg: pkg, args: args, env: env} }
	if id == catalog.Claude && (strings.Contains(slash, "/.local/share/claude/versions/") || strings.HasSuffix(strings.ToLower(slash), "/.local/bin/claude.exe")) || id == catalog.Codex && strings.Contains(slash, "/.codex/packages/standalone/") {
		return makePlan("self", s.packages[0], bin, "update")
	}
	for _, kind := range []string{"Cellar", "Caskroom"} {
		mark := "/" + kind + "/" + s.brew + "/"
		if i := strings.Index(slash, mark); i >= 0 {
			brew := filepath.FromSlash(slash[:i] + "/bin/brew")
			if !executable(brew) {
				return nil
			}
			p := makePlan("brew", s.brew, brew, "upgrade")
			p.cask = kind == "Caskroom"
			if p.cask {
				p.args = append(p.args, "--cask")
			}
			p.args = append(p.args, s.brew)
			return p
		}
	}
	for _, pkg := range s.packages {
		mark := "/node_modules/" + pkg + "/"
		i := strings.Index(slash, mark)
		if i < 0 {
			continue
		}
		modules := filepath.FromSlash(slash[:i] + "/node_modules")
		// Only a matching package directory proves ownership.
		var manifest struct{ Name string }
		b, err := os.ReadFile(filepath.Join(modules, filepath.FromSlash(pkg), "package.json"))
		if err != nil || json.Unmarshal(b, &manifest) != nil || manifest.Name != pkg {
			return nil
		}
		lower := strings.ToLower(slash)
		if strings.Contains(lower, "/.volta/") || strings.Contains(lower, "/yarn/") || strings.Contains(lower, "/.yarn/") {
			return nil
		}
		for _, via := range []string{"bun", "pnpm"} {
			owned := via == "bun" && strings.Contains(lower, "/.bun/") || via == "pnpm" && (strings.Contains(lower, "/pnpm/") || strings.Contains(lower, "/.pnpm/"))
			if owned {
				m := manager(via, filepath.Dir(bin))
				if m == "" {
					return nil
				}
				return makePlan(via, pkg, m, "add", "-g", pkg+"@latest")
			}
		}
		prefix := filepath.Dir(modules)
		if runtime.GOOS != "windows" {
			if filepath.Base(prefix) != "lib" {
				return nil
			}
			prefix = filepath.Dir(prefix)
		}
		dir := prefix
		if runtime.GOOS != "windows" {
			dir = filepath.Join(prefix, "bin")
		}
		npm := manager("npm", dir)
		if npm == "" {
			return nil
		}
		p := makePlan("npm", pkg, npm, "install", "-g", "--prefix", prefix, pkg+"@latest")
		p.prefix = filepath.Dir(npm)
		return p
	}
	return nil
}

var versionRE = regexp.MustCompile(`(?:^|[^\w.])v?(\d+\.\d+\.\d+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?)`)

func parseVersion(out string) string {
	m := versionRE.FindStringSubmatch(out)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}
func newer(a, b string) bool {
	if parseVersion(a) != a || parseVersion(b) != b {
		return false
	}
	an, ap, _ := strings.Cut(a, "-")
	bn, bp, _ := strings.Cut(b, "-")
	x, y := strings.Split(an, "."), strings.Split(bn, ".")
	for i := range x {
		p, _ := strconv.Atoi(x[i])
		q, _ := strconv.Atoi(y[i])
		if p != q {
			return p > q
		}
	}
	if ap == bp {
		return false
	}
	if ap == "" {
		return true
	}
	if bp == "" {
		return false
	}
	x, y = strings.Split(ap, "."), strings.Split(bp, ".")
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] == y[i] {
			continue
		}
		p, e := strconv.Atoi(x[i])
		q, f := strconv.Atoi(y[i])
		if e == nil && f == nil {
			return p > q
		}
		if e == nil {
			return false
		}
		if f == nil {
			return true
		}
		return x[i] > y[i]
	}
	return len(x) > len(y)
}

// boundedOutput also limits memory if an updater prints an unbounded log.
type boundedOutput struct{ data []byte }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	b.data = append(b.data, p...)
	if len(b.data) > 16<<10 {
		b.data = append([]byte(nil), b.data[len(b.data)-(16<<10):]...)
	}
	return n, nil
}
func run(ctx context.Context, args, env []string, prefix string) (string, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = processenv.Environ(args[0], env...)
	if prefix != "" {
		for _, entry := range cmd.Env {
			if strings.HasPrefix(entry, "PATH=") {
				cmd.Env = append(cmd.Env, "PATH="+prefix+string(os.PathListSeparator)+strings.TrimPrefix(entry, "PATH="))
				break
			}
		}
	}
	// Updaters may launch a child that inherits stdout; do not wait indefinitely.
	cmd.WaitDelay = 2 * time.Second
	var out boundedOutput
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return strings.TrimSpace(string(out.data)), err
}
func version(ctx context.Context, bin string, env []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out, err := run(ctx, []string{bin, "--version"}, env, filepath.Dir(bin))
	if err != nil {
		return "", fmt.Errorf("%s: %w", out, err)
	}
	v := parseVersion(out)
	if v == "" {
		return "", errors.New(tr("CLI did not report a version"))
	}
	return v, nil
}
func (p *plan) shown() string {
	a := append([]string{filepath.Base(p.args[0])}, p.args[1:]...)
	for i, v := range a {
		if strings.ContainsAny(v, " \t") {
			a[i] = strconv.Quote(v)
		}
	}
	return strings.Join(a, " ")
}

var registry = "https://registry.npmjs.org/"

func (p *plan) latest(ctx context.Context) (string, error) {
	if p.via == "brew" {
		args := []string{p.args[0], "info", "--json=v2"}
		if p.cask {
			args = append(args, "--cask")
		}
		args = append(args, p.pkg)
		out, err := run(ctx, args, append(append([]string(nil), p.env...), "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ENV_HINTS=1"), "")
		if err != nil {
			return "", err
		}
		var data struct {
			Formulae []struct{ Versions struct{ Stable string } }
			Casks    []struct{ Version string }
		}
		if err = json.Unmarshal([]byte(out), &data); err != nil {
			return "", err
		}
		if len(data.Formulae) > 0 {
			return parseVersion(data.Formulae[0].Versions.Stable), nil
		}
		if len(data.Casks) > 0 {
			return parseVersion(data.Casks[0].Version), nil
		}
		return "", errors.New(tr("No Homebrew version found"))
	}
	req, err := http.NewRequestWithContext(ctx, "GET", registry+p.pkg+"/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Miya")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Registry: %s", resp.Status)
	}
	var data struct{ Version string }
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data)
	return data.Version, err
}

func Check(ctx context.Context, id, bin string, env []string) Info {
	info := Info{Path: bin}
	if bin == "" {
		return info
	}
	v, err := version(ctx, bin, env)
	info.Version = v
	if err != nil {
		info.Error = err.Error()
		return info
	}
	p := installedPlan(id, bin, env)
	if p == nil {
		return info
	}
	info.Via, info.Command = p.via, p.shown()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	info.Latest, err = p.latest(ctx)
	if err != nil {
		info.Error = err.Error()
	} else if parseVersion(info.Latest) != info.Latest || info.Latest == "" {
		info.Latest = ""
		info.Error = tr("No release version found")
	}
	info.Update = newer(info.Latest, info.Version)
	return info
}

// Upgrade re-detects ownership at the time of the click, never executing a
// command received from the window. Its caller serializes package managers.
func Upgrade(ctx context.Context, id, bin string, env []string) Info {
	before := Check(ctx, id, bin, env)
	if !before.Update {
		if before.Error == "" {
			before.Error = tr("No supported update is available")
		}
		return before
	}
	p := installedPlan(id, bin, env)
	if p == nil || p.shown() != before.Command || p.via != before.Via {
		before.Error = tr("Installation changed; check again")
		return before
	}
	out, err := run(ctx, p.args, p.env, p.prefix)
	after := Check(ctx, id, bin, env)
	after.Previous = before.Version
	if err != nil {
		after.Error = fmt.Sprintf("%s: %v\n%s", p.shown(), err, out)
	} else if !newer(after.Version, before.Version) {
		after.Error = tr("The updater finished, but a newer installed version could not be verified")
	}
	return after
}
