package extractor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	codex "miya/internal/agent/codex/native"
	"miya/internal/memory"
)

// Query the installed CLI rather than assuming its feature registry. All known
// optional capabilities are disabled for the isolated extraction process.
func codexExtractionOverrides(ctx context.Context, bin, directory string) ([]string, error) {
	features, err := codexExtractionCommand(ctx, bin, directory, "features", "list")
	if err != nil {
		return nil, fmt.Errorf("Unable to verify Codex extraction isolation settings: %w", err)
	}
	servers, err := codexExtractionCommandEnv(ctx, codexOfflineEnv(codex.CommandEnv()), bin, directory, "mcp", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("Unable to verify Codex MCP isolation settings: %w", err)
	}
	overrides := []string{"web_search=\"disabled\"", "tools.view_image=false", "project_doc_max_bytes=0", "skills.include_instructions=false", "skills.bundled.enabled=false", "agents.enabled=false", "approval_policy=\"never\"", "developer_instructions=" + strconv.Quote(memoryExtractionSystem), "user_instructions=\"\""}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(features), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || strings.Contains(line, "removed") {
			continue
		}
		value := fields[len(fields)-1]
		if value != "true" && value != "false" {
			continue
		}
		name := fields[0]
		valid := true
		for _, r := range name {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.') {
				valid = false
			}
		}
		if !valid || seen[name] {
			continue
		}
		seen[name] = true
		overrides = append(overrides, "features."+name+"=false")
	}
	if !seen["shell_tool"] || !seen["hooks"] || !seen["multi_agent"] {
		return nil, errors.New("This Codex version does not support restricted memory extraction")
	}
	var list []struct {
		Name      string `json:"name"`
		Transport struct {
			Type string `json:"type"`
		} `json:"transport"`
	}
	if json.Unmarshal(servers, &list) != nil {
		return nil, errors.New("Unable to parse Codex MCP configuration")
	}
	var disabledServers []string
	for _, server := range list {
		if server.Name != "" {
			disabledServers = append(disabledServers, strconv.Quote(server.Name)+"="+codexDisabledMCPServer(server.Transport.Type))
		}
	}
	// Keep server identifiers inside a TOML value, not a dotted CLI key: names
	// themselves may contain dots. Replace the endpoint as well as disabling it.
	overrides = append(overrides, "mcp_servers={"+strings.Join(disabledServers, ",")+"}")
	return overrides, nil
}

// -c overrides merge into the user's server table rather than replacing it, so
// the replacement endpoint must match the configured transport: Codex rejects
// a URL server that also receives a stdio command, and the whole run fails to
// load its configuration. Older CLIs omit the transport; treat that as stdio.
func codexDisabledMCPServer(transport string) string {
	if transport == "" || transport == "stdio" {
		return `{enabled=false,command="miya-disabled-extractor-mcp"}`
	}
	return `{enabled=false,url="http://127.0.0.1:9/miya-disabled-extractor-mcp"}`
}

// codexOfflineEnv routes all CLI HTTP traffic to an unreachable proxy. Listing
// MCP servers probes OAuth discovery endpoints of every URL server; the list
// itself only needs local configuration, and extraction must not contact
// configured MCP servers even before the isolated run starts.
func codexOfflineEnv(env []string) []string {
	const unreachable = "http://127.0.0.1:9"
	out := make([]string, 0, len(env)+8)
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		switch strings.ToUpper(key) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		out = append(out, item)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		out = append(out, key+"="+unreachable)
	}
	return out
}

func codexExtractionCommand(ctx context.Context, bin, directory string, args ...string) ([]byte, error) {
	return codexExtractionCommandEnv(ctx, codex.CommandEnv(), bin, directory, args...)
}

func codexExtractionCommandEnv(ctx context.Context, env []string, bin, directory string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.WaitDelay = time.Second
	cmd.Dir = directory
	var out, stderr extractionOutput
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, codexCommandError(err, stderr.Bytes())
	}
	return out.Bytes(), nil
}

// codexCommandError keeps a short, single-line CLI diagnosis. Configuration
// and startup failures are otherwise invisible; the extractor input is passed
// on stdin and is not echoed by these errors, and the summary stays bounded.
func codexCommandError(err error, stderr []byte) error {
	summary := codexStderrSummary(stderr)
	if summary == "" {
		return err
	}
	return fmt.Errorf("%v: %s", err, summary)
}

func codexStderrSummary(stderr []byte) string {
	var parts []string
	for _, line := range strings.Split(string(stderr), "\n") {
		if line = strings.TrimSpace(line); line != "" && line != "Caused by:" {
			parts = append(parts, line)
		}
	}
	summary := strings.Join(parts, " ")
	if runes := []rune(summary); len(runes) > 300 {
		summary = string(runes[:300]) + "…"
	}
	return summary
}

func codexExtractionArgs(directory string, overrides []string) []string {
	args := []string{"exec", "--skip-git-repo-check", "--ephemeral", "--ignore-rules", "--sandbox", "read-only", "--json", "--output-schema", filepath.Join(directory, "schema.json"), "--output-last-message", filepath.Join(directory, "result.json")}
	for _, override := range overrides {
		args = append(args, "-c", override)
	}
	return append(args, "-")
}

func WithCodex(ctx context.Context, job memory.ExtractionJob) ([]memory.Candidate, error) {
	bin, err := codex.Binary()
	if err != nil {
		return nil, errors.New("Codex extractor unavailable")
	}
	dir, err := os.MkdirTemp("", "miya-memory-codex-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	overrides, err := codexExtractionOverrides(ctx, bin, dir)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(dir, "schema.json"), []byte(memoryExtractionSchema), 0600); err != nil {
		return nil, err
	}
	input, err := job.InputJSON()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, codexExtractionArgs(dir, overrides)...)
	cmd.Env = codex.CommandEnv()
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader(string(input))
	var out, stderr extractionOutput
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err = cmd.Run(); err != nil {
		return nil, fmt.Errorf("Codex extraction failed: %w", codexCommandError(err, stderr.Bytes()))
	}
	if err = validateCodexExtractionEvents(out.Bytes()); err != nil {
		return nil, err
	}
	file, err := os.Open(filepath.Join(dir, "result.json"))
	if err != nil {
		return nil, errors.New("Codex returned no extraction result")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, errors.New("Codex extraction result exceeds the limit")
	}
	return decodeMemoryCandidates(raw)
}

func validateCodexExtractionEvents(raw []byte) error {
	completed := false
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var event struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
			} `json:"item"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			return errors.New("Unable to parse Codex extraction event")
		}
		if event.Type == "turn.failed" || event.Type == "error" {
			return errors.New("Codex extraction turn failed")
		}
		if event.Type == "turn.completed" {
			completed = true
		}
		// A nonterminal diagnostic item is not an operation. The CLI must still
		// report a successful completed turn and return a valid final schema.
		if strings.HasPrefix(event.Type, "item.") && event.Item.Type != "agent_message" && event.Item.Type != "reasoning" && event.Item.Type != "error" {
			kind := event.Item.Type
			for _, r := range kind {
				if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_' || r >= '0' && r <= '9') {
					kind = "unknown"
					break
				}
			}
			if len(kind) > 64 {
				kind = "unknown"
			}
			return fmt.Errorf("Unexpected Codex extraction event type %s. Result discarded.", kind)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !completed {
		return errors.New("Codex extraction did not complete")
	}
	return nil
}
