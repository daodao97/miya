// Package config is how Miya hands its MCP server to the agents it runs:
// the arguments and environment that make Codex and Claude Code start
// miya -mcp, and the tools a delegated conversation may only read.
package config

import (
	"encoding/json"
	"os"
	"strings"

	"miya/internal/processenv"
	svcproto "miya/internal/service/protocol"
)

// claudeMCPArgs give a Claude Code run the miya server; its read-only tools
// run without asking, delegate asks as any tool does.
func ClaudeArgs(cwd string) []string {
	exe, ok := Command()
	if !ok {
		return nil
	}
	exe, args := agentCommand(exe, cwd)
	cfg, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"miya": map[string]any{"type": "stdio", "command": exe, "args": args}}})
	allowed := make([]string, len(ReadOnlyTools))
	for i, tool := range ReadOnlyTools {
		allowed[i] = "mcp__miya__" + tool
	}
	return []string{"--mcp-config", string(cfg), "--allowedTools", strings.Join(allowed, ",")}
}

// claudeMCPEnv lets a delegation wait for its reply.
func ClaudeEnv() []string {
	if _, ok := Command(); !ok {
		return nil
	}
	return []string{"MCP_TOOL_TIMEOUT=1500000"}
}

// codexMCPArgs give a Codex app-server the miya server, as config overrides.
func CodexArgs(cwd string) []string {
	exe, ok := Command()
	if !ok {
		return nil
	}
	exe, args := agentCommand(exe, cwd)
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = TomlString(a)
	}
	return []string{
		"-c", "mcp_servers.miya.command=" + TomlString(exe),
		"-c", "mcp_servers.miya.args=[" + strings.Join(quoted, ",") + "]",
		"-c", "mcp_servers.miya.tool_timeout_sec=1500",
	}
}

func Command() (string, bool) {
	if !Enabled {
		return "", false
	}
	exe, err := os.Executable()
	return exe, err == nil
}

// miyaReadOnlyTools are the miya tools an agent may call without asking.
var ReadOnlyTools = []string{"list_threads", "read_thread", "thread_status", "list_agents", "read_task", "wait_task", "list_tasks", "list_automations", "report_result", "search_memory", "runtime_info", "request_desktop_control"}

// tomlString quotes s as a TOML basic string, whose escapes JSON's are.
func TomlString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// miyaMCPEnabled is set in the background service, whose socket the miya
// MCP server reaches; agents get the server only there.
var Enabled bool

// WSL starts the Windows MCP executable through interop. Only the executable
// is translated: its -mcp arguments still address the Windows service/files.
func agentCommand(exe, cwd string) (string, []string) {
	args := []string{"-mcp", cwd, svcproto.Socket()}
	if processenv.AgentWSL() != "" {
		return "sh", append([]string{"-c", `exe=$(wslpath -a -- "$1") || exit $?; shift; exec "$exe" "$@"`, "miya-mcp", exe}, args...)
	}
	return exe, args
}
