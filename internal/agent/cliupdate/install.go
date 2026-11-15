package cliupdate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"

	"miya/internal/agent/catalog"
	"miya/internal/processenv"
)

var findInstalled = processenv.LookPath

func installPlan(ctx context.Context, id, bin string, env []string) (*plan, error) {
	s, ok := specs[id]
	if !ok {
		return nil, fmt.Errorf("unknown agent: %s", id)
	}
	p := &plan{pkg: s.packages[0], env: env}
	t, wsl := processenv.WSL(bin)
	if id == catalog.Claude {
		p.via = "self"
		p.scriptURL = "https://claude.ai/install.sh"
		if wsl {
			p.args = []string{processenv.WSLBinary(t.Distribution, "bash"), "-s"}
		} else if runtime.GOOS == "windows" {
			shell, err := processenv.LookPath("powershell.exe")
			if err != nil {
				return nil, err
			}
			p.args = []string{shell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", "-"}
			p.scriptURL = "https://claude.ai/install.ps1"
		} else {
			shell, err := processenv.LookPath("bash")
			if err != nil {
				return nil, err
			}
			p.args = []string{shell, "-s"}
		}
		return p, nil
	}
	npm, prefix := manager("npm", ""), processenv.ManagedBinDir()
	if runtime.GOOS != "windows" {
		prefix = filepath.Dir(prefix)
	}
	if wsl {
		out, err := wslRun(ctx, t, `command -v npm || exit 127; printf '%s\n' "$HOME/.local/share/miya/agents/npm"`, nil, env)
		if err != nil {
			if id == catalog.Codex && isMissingWSL(err) {
				p.via, p.scriptURL, p.args = "self", "https://chatgpt.com/codex/install.sh", []string{processenv.WSLBinary(t.Distribution, "sh"), "-s"}
				return p, nil
			}
			return nil, fmt.Errorf("%s", tr("Install Node.js and npm in the selected environment first"))
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) < 2 {
			return nil, fmt.Errorf("npm did not report its installation directory")
		}
		npm, prefix = processenv.WSLBinary(t.Distribution, lines[len(lines)-2]), lines[len(lines)-1]
	}
	if npm == "" {
		if id == catalog.Codex && runtime.GOOS != "windows" && !wsl {
			shell, err := processenv.LookPath("sh")
			if err != nil {
				return nil, err
			}
			p.via, p.scriptURL, p.args = "self", "https://chatgpt.com/codex/install.sh", []string{shell, "-s"}
			return p, nil
		}
		return nil, fmt.Errorf("%s", tr("Install Node.js and npm in the selected environment first"))
	}
	p.via, p.args = "npm", []string{npm, "install", "-g", "--prefix", prefix, p.pkg + "@latest"}
	if !wsl {
		p.prefix = filepath.Dir(npm)
	}
	return p, nil
}

func missingInfo(ctx context.Context, id, bin string, env []string) Info {
	info := Info{Path: bin, Install: true}
	if _, ok := specs[id]; !ok {
		return Info{Path: bin}
	}
	p, err := installPlan(ctx, id, bin, env)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	info.Command, info.Via = p.shown(), p.via
	return info
}

// install never replaces an existing executable. It uses a user-writable npm
// prefix owned by Miya, or the vendor's native installer, then probes again.
func install(ctx context.Context, id, bin string, env []string) Info {
	p, err := installPlan(ctx, id, bin, env)
	if err != nil {
		return Info{Path: bin, Install: true, Error: err.Error()}
	}
	info := Info{Path: bin, Install: true, Command: p.shown(), Via: p.via}
	if bin != "" && !processenv.IsWSL(bin) {
		info.Error = tr("Installation changed; check again")
		return info
	}
	if t, ok := processenv.WSL(bin); ok {
		if _, err := wslResolve(ctx, t, env); err == nil {
			return Check(ctx, id, bin, env)
		} else if !isMissingWSL(err) {
			info.Error = err.Error()
			return info
		}
	} else if d, ok := catalog.Lookup(id); ok {
		if found, err := findInstalled(d.Command); err == nil {
			return Check(ctx, id, found, env)
		}
	}
	var input string
	if p.scriptURL != "" {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, p.scriptURL, nil)
		if e != nil {
			info.Error = e.Error()
			return info
		}
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			info.Error = e.Error()
			return info
		}
		b, e := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if e != nil || resp.StatusCode != http.StatusOK || len(b) == 0 || len(b) >= 4<<20 {
			info.Error = fmt.Sprintf("Installer download failed: %s (%v)", resp.Status, e)
			return info
		}
		input = string(b)
	}
	out, runErr := runInput(ctx, p.args, p.env, p.prefix, input)
	path := bin
	if !processenv.IsWSL(bin) {
		d, _ := catalog.Lookup(id)
		path, _ = findInstalled(d.Command)
	}
	after := Check(ctx, id, path, env)
	if after.Version == "" || after.Install {
		if runErr != nil {
			after.Error = fmt.Sprintf("%s: %v\n%s", p.shown(), runErr, out)
		} else {
			after.Error = tr("The installer finished, but the installed CLI could not be verified")
		}
	}
	return after
}

// Environment describes the service host, so a macOS window connected to a
// Windows service offers that service's WSL setting rather than its own OS.
type Environment struct {
	Platform string `json:"platform,omitempty"`
	WSL      string `json:"wsl,omitempty"`
	Busy     bool   `json:"busy,omitempty"`
}
