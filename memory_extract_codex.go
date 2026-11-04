package main

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

	"miya/internal/codex"
	"miya/internal/memory"
)

// Query the installed CLI rather than assuming its feature registry. All known
// optional capabilities are disabled for the isolated extraction process.
func codexExtractionOverrides(ctx context.Context, bin, directory string) ([]string, error) {
	features, err := codexExtractionCommand(ctx, bin, directory, "features", "list")
	if err != nil {
		return nil, errors.New("Unable to verify Codex extraction isolation settings")
	}
	servers, err := codexExtractionCommand(ctx, bin, directory, "mcp", "list", "--json")
	if err != nil {
		return nil, errors.New("Unable to verify Codex MCP isolation settings")
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
		Name string `json:"name"`
	}
	if json.Unmarshal(servers, &list) != nil {
		return nil, errors.New("Unable to parse Codex MCP configuration")
	}
	var disabledServers []string
	for _, server := range list {
		if server.Name != "" {
			disabledServers = append(disabledServers, strconv.Quote(server.Name)+`={enabled=false,command="miya-disabled-extractor-mcp"}`)
		}
	}
	// Keep server identifiers inside a TOML value, not a dotted CLI key: names
	// themselves may contain dots. Replace commands as well as disabling them.
	overrides = append(overrides, "mcp_servers={"+strings.Join(disabledServers, ",")+"}")
	return overrides, nil
}

func codexExtractionCommand(ctx context.Context, bin, directory string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = codex.CommandEnv()
	cmd.WaitDelay = time.Second
	cmd.Dir = directory
	var out extractionOutput
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func codexExtractionArgs(directory string, overrides []string) []string {
	args := []string{"exec", "--skip-git-repo-check", "--ephemeral", "--ignore-rules", "--sandbox", "read-only", "--json", "--output-schema", filepath.Join(directory, "schema.json"), "--output-last-message", filepath.Join(directory, "result.json")}
	for _, override := range overrides {
		args = append(args, "-c", override)
	}
	return append(args, "-")
}

func extractMemoryWithCodex(ctx context.Context, job memory.ExtractionJob) ([]memory.Candidate, error) {
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
	var out extractionOutput
	cmd.Stdout = &out
	if err = cmd.Run(); err != nil {
		return nil, errors.New("Codex extraction failed")
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
