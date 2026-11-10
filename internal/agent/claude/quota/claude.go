package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"miya/internal/agent/cli"
	"miya/internal/agent/quota"
)

// As in Magpie, /usage is handled by Claude Code itself. No prompt, tools,
// project settings, MCP servers or persisted conversation are involved.
var claudeArgs = []string{"-p", "/usage", "--output-format", "json", "--tools", "", "--strict-mcp-config", "--setting-sources", "", "--no-session-persistence"}

func readClaude(ctx context.Context, binary, home string) ([]quota.Window, error) {
	dir, err := os.MkdirTemp("", "miya-usage-")
	if err != nil {
		return nil, errors.New("Unable to create the usage directory")
	}
	defer os.RemoveAll(dir)
	cmd := exec.CommandContext(ctx, binary, claudeArgs...)
	cmd.Dir, cmd.Env, cmd.Stderr = dir, cli.CommandEnv("claude", home, binary), io.Discard
	cmd.WaitDelay = time.Second
	cli.IsolateReader(cmd)
	var out limitedBuffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, errors.New("Failed to read Claude usage. Check CLI sign-in.")
	}
	return parseClaude(out.Bytes())
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, errors.New("usage output too large")
	}
	return b.Buffer.Write(p)
}

var claudeLine = regexp.MustCompile(`^Current (session|week(?: \(([^)]+)\))?):\s*([0-9.]+)% used(?:\s*·\s*resets (.+))?$`)
var ansiUsage = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]")
var claudeDenied = regexp.MustCompile(`(?i)\b(401|403)\b|not (logged|signed) in|signed out|unauthorized|forbidden|authentication (failed|required)|invalid (access )?token`)

func parseClaude(data []byte) ([]quota.Window, error) {
	type result struct {
		Type    string `json:"type"`
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
	}
	var r result
	if json.Unmarshal(data, &r) != nil {
		var all []result
		if json.Unmarshal(data, &all) != nil {
			return nil, errors.New("Unrecognized Claude usage data")
		}
		for _, item := range all {
			if item.Type == "result" {
				r = item
			}
		}
	}
	text := ansiUsage.ReplaceAllString(r.Result, "")
	if r.IsError || claudeDenied.MatchString(text) {
		return nil, errors.New("Claude returned no quota. Check CLI sign-in.")
	}
	var windows []quota.Window
	seen := make(map[string]bool)
	for _, line := range strings.Split(text, "\n") {
		m := claudeLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil || m[2] != "" && !strings.EqualFold(m[2], "all models") {
			continue
		}
		name := "5 hours"
		if m[1] != "session" {
			name = "7 days"
		}
		used, err := strconv.ParseFloat(m[3], 64)
		if err != nil || seen[name] {
			continue
		}
		seen[name] = true
		windows = append(windows, quota.Window{Name: name, Used: quota.Percent(used), Reset: m[4]})
	}
	if len(windows) == 0 {
		return nil, errors.New("Subscription quota is unavailable for this account")
	}
	return windows, nil
}
