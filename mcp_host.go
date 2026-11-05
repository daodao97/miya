package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"miya/internal/textutil"
	"os"
	"path/filepath"
	"strings"
)

// The service's side of the miya MCP server: the conversations it lists,
// reads and hands work to, and the arguments that give agents the server.

// miyaMCPEnabled is set in the background service, whose socket the miya
// MCP server reaches; agents get the server only there.
var miyaMCPEnabled bool

// maxDelegationDepth bounds how far conversations hand work on: one that
// was handed work this many times over hands none further.
const maxDelegationDepth = 2

// miyaReadOnlyTools are the miya tools an agent may call without asking.
var miyaReadOnlyTools = []string{"list_threads", "read_thread", "thread_status", "list_agents", "read_task", "wait_task", "list_tasks", "list_automations", "report_result", "search_memory", "runtime_info", "request_desktop_control"}

func miyaMCPCommand() (string, bool) {
	if !miyaMCPEnabled {
		return "", false
	}
	exe, err := os.Executable()
	return exe, err == nil
}

// claudeMCPArgs give a Claude Code run the miya server; its read-only tools
// run without asking, delegate asks as any tool does.
func claudeMCPArgs(cwd string) []string {
	exe, ok := miyaMCPCommand()
	if !ok {
		return nil
	}
	cfg, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"miya": map[string]any{"type": "stdio", "command": exe, "args": []string{"-mcp", cwd, serviceSocket()}}}})
	allowed := make([]string, len(miyaReadOnlyTools))
	for i, tool := range miyaReadOnlyTools {
		allowed[i] = "mcp__miya__" + tool
	}
	return []string{"--mcp-config", string(cfg), "--allowedTools", strings.Join(allowed, ",")}
}

// claudeMCPEnv lets a delegation wait for its reply.
func claudeMCPEnv() []string {
	if _, ok := miyaMCPCommand(); !ok {
		return nil
	}
	return []string{"MCP_TOOL_TIMEOUT=1500000"}
}

// codexMCPArgs give a Codex app-server the miya server, as config overrides.
func codexMCPArgs(cwd string) []string {
	exe, ok := miyaMCPCommand()
	if !ok {
		return nil
	}
	return []string{
		"-c", "mcp_servers.miya.command=" + tomlString(exe),
		"-c", "mcp_servers.miya.args=[" + tomlString("-mcp") + "," + tomlString(cwd) + "," + tomlString(serviceSocket()) + "]",
		"-c", "mcp_servers.miya.tool_timeout_sec=1500",
	}
}

// tomlString quotes s as a TOML basic string, whose escapes JSON's are.
func tomlString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// mcpHandle answers a miya tool call the service received.
func (c *chatController) mcpHandle(op string, r mcpRequest) (any, error) {
	if op == "mcpCollaboration" {
		return c.collaborationMCP(r)
	}
	if c.collaboration != nil && (op == "mcpThreads" || op == "mcpRead" || op == "mcpStatus" || op == "mcpDelegate" || op == "mcpLegacyTask") {
		if len(r.Arguments) == 0 {
			return nil, errors.New("CALLER_UNAVAILABLE: Restart the agent to use verified conversation tools")
		}
		caller, _, err := c.mcpCallerWithArguments(r.Tool, r.Arguments)
		if err != nil {
			return nil, err
		}
		if op == "mcpThreads" {
			return c.mcpThreadsFor(caller, c.threadWorkspace(caller.ID)), nil
		}
		if op == "mcpDelegate" {
			return c.legacyDelegate(r, caller)
		}
		if op == "mcpLegacyTask" {
			return c.legacyTaskRead(r, caller)
		}
	}
	if op == "mcpRuntime" {
		return c.mcpRuntime(r)
	}
	if r.Reference != "" {
		ref, ok := parseThreadURL(r.Reference)
		if !ok || ref.Source != c.referenceSource {
			return nil, errors.New("Session link is invalid or belongs to another desktop")
		}
		r.Thread = 0
		for _, t := range c.allThreads() {
			if c.threadRef(t) == ref {
				r.Thread = t.ID
				break
			}
		}
		if r.Thread == 0 {
			if _, known := c.forkedReferences[ref]; known && op == "mcpRead" {
				t, messages, err := c.resolveReference(ref)
				if err != nil {
					if c.referenceLoading[ref] {
						return mcpReadResult{}, nil
					}
					return nil, err
				}
				return mcpReadResult{Loaded: true, Packet: threadPacket(t, messages)}, nil
			}
			return nil, errors.New("The conversation no longer exists or is unavailable")
		}
	}
	// The MCP server's workspace argument is not trusted: Codex runs one
	// app-server for every project, started wherever the service was. The
	// calling conversation's own project is used instead.
	switch op {
	case "mcpDesktopControl":
		return c.mcpDesktopControl(r)
	case "mcpMemory":
		return c.mcpMemory(r)
	case "mcpThreads":
		return c.mcpThreads(r), nil
	case "mcpRead":
		return c.mcpRead(r)
	case "mcpStatus":
		return c.mcpStatus(r)
	case "mcpDelegate":
		return c.mcpDelegate(r)
	case "mcpReportResult":
		return c.mcpReportResult(r)
	case "mcpAutomationList", "mcpAutomationCreate", "mcpAutomationUpdate":
		return c.mcpAutomation(op, r)
	}
	return nil, fmt.Errorf("unknown miya call %s", op)
}

// allThreads are the conversations of every project, the open one first.
func (c *chatController) allThreads() []Thread {
	out := append([]Thread(nil), c.threads()...)
	for path, threads := range c.projectCache {
		if path != c.cwd {
			out = append(out, threads...)
		}
	}
	return out
}

// projectThreads are the conversations of one project.
func (c *chatController) projectThreads(project string) []Thread {
	if project == "" || project == c.cwd {
		return c.threads()
	}
	return c.projectCache[project]
}

// mcpCaller is the conversation making a miya tool call: the one whose
// call of it is in progress, whichever agent runs it.
func (c *chatController) mcpCaller(tool string) (Thread, bool) {
	var caller Thread
	var at int64
	found := false
	// The caller may be in a background project.
	for _, t := range c.allThreads() {
		for i := len(t.Messages) - 1; i >= 0; i-- {
			m := t.Messages[i]
			if (m.Status == "inProgress" || m.Status == "in_progress") && isMiyaToolCall(m, tool) {
				if !found || m.StartedAtMs > at {
					caller, at, found = t, m.StartedAtMs, true
				}
				break
			}
		}
	}
	return caller, found
}

func isMiyaToolCall(m Message, tool string) bool {
	switch strings.ToLower(m.Kind) {
	case "claudetool":
		name, _ := claudeToolCall(m)
		return name == "mcp__miya__"+tool
	case "mcptoolcall":
		return m.Item != nil && m.Item.Server == "miya" && m.Item.Tool == tool
	}
	return false
}

func (c *chatController) mcpThreads(r mcpRequest) mcpThreadsResult {
	caller, ok := c.mcpCaller(r.Tool)
	project := c.cwd
	if ok {
		project = c.threadWorkspace(caller.ID)
	}
	return c.mcpThreadsFor(caller, project)
}

func (c *chatController) mcpThreadsFor(caller Thread, project string) mcpThreadsResult {
	res := mcpThreadsResult{Threads: []mcpThreadInfo{}}
	for _, t := range c.projectThreads(project) {
		if t.ID == 0 || t.ID == caller.ID || !c.threadRef(t).Valid() {
			continue
		}
		status := "idle"
		if t.Busy || t.Loading {
			status = subAgentRunning
		}
		res.Threads = append(res.Threads, mcpThreadInfo{Reference: c.threadRef(t).URL(), ID: t.ID, Title: threadRefTitle(t), Agent: threadAgent(t), Status: status})
	}
	return res
}

func (c *chatController) mcpRead(r mcpRequest) (mcpReadResult, error) {
	t, messages, ok := c.resolveThreadRef(r.Thread)
	if !ok {
		return mcpReadResult{}, errors.New("Session not found. Use list_threads to see available session links.")
	}
	loaded := t.HistoryLoaded
	if !loaded {
		c.prefetchThread(t.ID)
	}
	return mcpReadResult{Loaded: loaded, Packet: threadPacket(t, messages)}, nil
}

func (c *chatController) mcpStatus(r mcpRequest) (mcpStatusResult, error) {
	t, _, ok := c.resolveThreadRef(r.Thread)
	if !ok {
		return mcpStatusResult{}, errors.New("The conversation no longer exists or is unavailable")
	}
	status, _ := c.handoffState(t.ID)
	last := -1
	for i, m := range t.Messages {
		if m.Role == "user" {
			last = i
		}
	}
	return mcpStatusResult{Reference: c.threadRef(t).URL(), ID: t.ID, Title: threadRefTitle(t), Agent: threadAgent(t), Status: status, Reply: textutil.ClipRunes(lastReply(t.Messages[last+1:]), 4000)}, nil
}

// mcpDelegate sends a task to another conversation, new or existing, with
// the caller's context as a reference, so the caller follows the work.
func (c *chatController) mcpDelegate(r mcpRequest) (mcpDelegateResult, error) {
	caller, ok := c.mcpCaller(r.Tool)
	if !ok {
		return mcpDelegateResult{}, errors.New(mcpCallerUnknown)
	}
	task := strings.TrimSpace(r.Task)
	if task == "" {
		return mcpDelegateResult{}, errors.New("task cannot be empty")
	}
	if c.delegationDepth(caller.ID) >= maxDelegationDepth {
		return mcpDelegateResult{}, fmt.Errorf("Delegation is limited to %d levels. This conversation is itself a delegated task; complete it directly.", maxDelegationDepth)
	}
	if r.Thread != 0 {
		if r.Thread == caller.ID {
			return mcpDelegateResult{}, errors.New("Cannot delegate a task to yourself")
		}
		if c.delegatedFrom(caller.ID, r.Thread) {
			return mcpDelegateResult{}, errors.New("The target session delegated this task to you. You cannot delegate it back.")
		}
		if _, _, ok := c.resolveThreadRef(r.Thread); !ok {
			return mcpDelegateResult{}, errors.New("The conversation no longer exists or is unavailable")
		}
	}
	agent := threadAgent(caller)
	if r.Thread == 0 && r.Agent != "" {
		if agent = normalizeAgentID(r.Agent); agent == "" {
			return mcpDelegateResult{}, fmt.Errorf("Agent %q not found. Choose %s or %s.", r.Agent, agentCodex, agentClaude)
		}
	}
	text := task
	if !r.NoContext {
		ref := c.threadRef(caller)
		if !ref.Valid() {
			return mcpDelegateResult{}, errors.New("The session must be created before its context can be handed off")
		}
		text += "\n\n" + referenceText(ref, threadRefTitle(caller))
	}
	id := c.submit(SendRequest{ThreadID: r.Thread, Agent: agent, Project: c.threadWorkspace(caller.ID), Text: text, MemoryControl: true})
	if id == 0 {
		return mcpDelegateResult{}, errors.New(textutil.FirstNonEmpty(c.lastSubmitError, "Handoff failed: the target agent is not ready, or the conversation is read-only"))
	}
	c.noteHandoffs([]Thread{caller}, id, threadRefTask(task))
	if c.delegationParent == nil {
		c.delegationParent = map[int]int{}
	}
	if _, ok := c.delegationParent[id]; !ok && id != caller.ID {
		c.delegationParent[id] = caller.ID
	}
	return mcpDelegateResult{Reference: c.threadRef(c.threadWithID(id)).URL(), Thread: id, Agent: threadAgent(c.threadWithID(id))}, nil
}

func normalizeAgentID(id string) string {
	normalized := strings.ToLower(strings.TrimSpace(id))
	if registeredBackend(normalized) {
		return normalized
	}
	switch normalized {
	case agentCodex:
		return agentCodex
	case agentClaude, "claude", "claude code":
		return agentClaude
	}
	return ""
}

// delegationDepth is how many hand-offs led to a conversation.
func (c *chatController) delegationDepth(id int) int {
	depth := 0
	for seen := map[int]bool{id: true}; ; {
		parent, ok := c.delegationParent[id]
		if !ok || seen[parent] {
			return depth
		}
		seen[parent] = true
		id, depth = parent, depth+1
	}
}

// delegatedFrom reports whether ancestor handed work, directly or through
// others, to id.
func (c *chatController) delegatedFrom(id, ancestor int) bool {
	for seen := map[int]bool{id: true}; ; {
		parent, ok := c.delegationParent[id]
		if !ok || seen[parent] {
			return false
		}
		if parent == ancestor {
			return true
		}
		seen[parent] = true
		id = parent
	}
}

// samePath reports whether two paths name one directory, through symlinks
// such as macOS's /var to /private/var.
func samePath(a, b string) bool {
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	return resolve(a) == resolve(b)
}

func (c *chatController) mcpRuntime(r mcpRequest) (any, error) {
	var caller Thread
	count := 0
	for _, t := range c.allThreads() {
		for _, message := range t.Messages {
			if (message.Status == "inProgress" || message.Status == "in_progress") && isMiyaToolCall(message, "runtime_info") {
				caller = t
				count++
				break
			}
		}
	}
	if count == 0 {
		return nil, errors.New(mcpCallerUnknown)
	}
	if count != 1 {
		return nil, errors.New("Multiple conversations are querying runtime information. Unable to identify the caller uniquely. Try again shortly.")
	}
	work, err := c.prepareRuntime(c.threadRef(caller), c.threadWorkspace(caller.ID))
	if err != nil {
		return nil, err
	}
	return deferred(func() (any, error) {
		data, err := work()
		if err != nil {
			return nil, err
		}
		return data.(conversationRuntime).View, nil
	}), nil
}
