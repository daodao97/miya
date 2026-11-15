package processenv

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"
)

type wslLayout struct{ Distribution, Home, CodexHome, ClaudeHome, MountRoot string }
type layoutEntry struct {
	layout wslLayout
	at     time.Time
}

var wslLayouts sync.Map

// Query only directory names, never account files. Cache the layout so history
// and configuration readers don't start a WSL process for each file.
func layoutWSL(ctx context.Context, distribution string) (wslLayout, error) {
	if cached, ok := wslLayouts.Load(distribution); ok {
		entry := cached.(layoutEntry)
		if time.Since(entry.at) < time.Minute {
			return entry.layout, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	binary := WSLBinary(distribution, "sh")
	cmd := CommandContext(ctx, binary, "-c", `printf '%s\n%s\n%s\n%s\n%s\n' "$WSL_DISTRO_NAME" "$HOME" "${CODEX_HOME:-$HOME/.codex}" "${CLAUDE_CONFIG_DIR:-$HOME/.claude}" "$(wslpath -a 'C:\')"`)
	Prepare(cmd)
	out, err := cmd.Output()
	if err != nil {
		return wslLayout{}, fmt.Errorf("WSL directories: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 5 || lines[0] == "" || !strings.HasPrefix(lines[1], "/") {
		return wslLayout{}, fmt.Errorf("WSL did not report its Linux directories")
	}
	layout := wslLayout{Distribution: lines[0], Home: lines[1], CodexHome: lines[2], ClaudeHome: lines[3], MountRoot: path.Dir(strings.TrimRight(lines[4], "/"))}
	wslLayouts.Store(distribution, layoutEntry{layout, time.Now()})
	return layout, nil
}

// WSLConfigDir is a Windows-accessible path into the selected Linux account.
func WSLConfigDir(agent, override string) (string, error) {
	binary, err := AgentBinary(agent)
	if err != nil {
		return "", err
	}
	t, ok := WSL(binary)
	if !ok {
		return override, nil
	}
	layout, err := layoutWSL(context.Background(), t.Distribution)
	if err != nil {
		return "", err
	}
	dir := layout.ClaudeHome
	if agent == "codex" {
		dir = layout.CodexHome
	}
	if override != "" {
		dir = linuxPath(layout.Distribution, layout.MountRoot, override)
	}
	return windowsPath(layout.Distribution, layout.MountRoot, dir), nil
}

func WSLProjectPath(cwd string) string {
	if AgentWSL() == "" {
		return cwd
	}
	distribution := AgentWSL()
	if distribution == "*" {
		distribution = ""
	}
	// Directory readers are called on worker goroutines. A failed lookup must
	// never cause them to use Windows' native account files.
	layout, err := layoutWSL(context.Background(), distribution)
	if err != nil {
		return cwd
	}
	return linuxPath(layout.Distribution, layout.MountRoot, cwd)
}
