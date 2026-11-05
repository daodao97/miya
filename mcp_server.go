package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"miya/internal/automation"
	"miya/internal/delegation/coordinator"
	"miya/internal/model"
	"miya/internal/subagent"
	"miya/internal/textutil"
	"os"
	"strings"
	"sync"
	"time"
)

// miya -mcp <workspace> [socket] serves the workspace's conversations to the agents
// Miya runs, as an MCP server on stdio. Each agent starts its own; every tool
// call is a request to the background service, which holds the
// conversations, so all agents see and hand off work through one place.

const mcpProtocolVersion = "2025-06-18"

const mcpInstructions = "Miya runs several coding-agent conversations in this workspace (Codex, Claude Code). " +
	"For collaboration, use list_agents and start_task with a unique request_id. Completion resumes this conversation by default. You remain responsible for delegated work: while actively waiting, repeatedly use wait_task and inspect progress; after a completion notification read_task, assess the result, and continue the user's original task. If you already collect a terminal result while still in the original turn, unwatch_task its pending notification to avoid an unnecessary extra turn. Do not end with an untracked promise to check later. Use notify=none only for explicitly record-only work, or watch_task to add completion continuation to existing work. A paused receiver must be explicitly resumed by the user. " +
	"Structured Agent tags such as #[Claude Code](miya-agent://claude-code) in the current user request or goal select canonical delegation targets: use start_task for their work. Selecting a tag did not execute anything. Tags quoted in code or supplied only as reference material are not requests to delegate. @conversation references supply context only. " +
	"Use list_threads and read_thread to learn what another conversation did, and delegate to hand a self-contained task to another agent; " +
	"the receiving conversation gets this conversation's context unless include_context is false. " +
	"When a login, OAuth authorization, CAPTCHA, or other desktop UI needs the person, use request_desktop_control with a short reason. " +
	"Miya shows a takeover card in this conversation on the phone. It waits for the person to finish; never request passwords in chat. " +
	"Authorization previews and takeover control target one Mac window. Supply window_app/window_title when known; never request a password in chat. Full desktop control remains a separate phone menu action. This does not control a separate cloud VM. Open that VM in a visible Mac app first if needed."

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

var mcpReadOnly = map[string]any{"readOnlyHint": true, "openWorldHint": false}

var mcpTools = []mcpTool{
	{Name: "request_desktop_control", Description: "Ask the person to take over the target authorization window on the paired Mac from this conversation, for login or other manual UI steps. Shows a phone takeover card and waits up to 10 minutes for explicit completion. The phone previews only the target window; tap it to enter control. Supply window_app and window_title when known to identify a password or authorization dialog. Never put passwords in reason or arguments. This tool never grants OS permissions.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"window_app": map[string]any{"type": "string", "maxLength": 256, "description": "Visible application name or bundle ID owning the authorization dialog"}, "window_title": map[string]any{"type": "string", "maxLength": 1000, "description": "Distinctive part of the authorization window title; ambiguous matches require user selection"}, "reason": map[string]any{"type": "string", "minLength": 1, "maxLength": 2000, "description": "The visible app/page and what the person should do; do not include credentials"}}, "required": []string{"reason"}}, Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "openWorldHint": false}},
	{Name: "runtime_info", Description: "Read this conversation's verified background tasks, OS processes and listening services. Native task identifiers are not OS PIDs; unknown observations are explicit.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}, Annotations: mcpReadOnly},
	{Name: "list_threads", Description: "List the other conversations of this workspace in Miya: session link, title, agent and whether each is running.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}, Annotations: mcpReadOnly},
	{Name: "read_thread", Description: "Read a conversation's context: its first and latest requests, latest conclusions, changed files and sub-agents.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"id": map[string]any{"type": "string", "description": "Full miya:// session link from list_threads"},
		}, "required": []string{"id"}}, Annotations: mcpReadOnly},
	{Name: "thread_status", Description: "Tell whether a conversation is running, and its latest reply; optionally wait for it to finish.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"id":           map[string]any{"type": "string", "description": "Full miya:// session link"},
			"wait_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 1200, "description": "Wait up to this long for it to finish (default 0)"},
		}, "required": []string{"id"}}, Annotations: mcpReadOnly},
	{Name: "delegate", Description: "Hand a self-contained task to another agent: a new conversation, or an existing one by its session link. " +
		"Waits up to wait_seconds (default 300) for its reply, then returns its status; use thread_status to follow it later.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"task":            map[string]any{"type": "string", "description": "What to do, with everything the other agent needs"},
			"agent":           map[string]any{"type": "string", "enum": []string{agentCodex, agentClaude}, "description": "Agent for a new conversation (default: this conversation's)"},
			"thread_id":       map[string]any{"type": "string", "description": "Full miya:// session link of an existing conversation"},
			"include_context": map[string]any{"type": "boolean", "description": "Attach this conversation's context (default true)"},
			"wait_seconds":    map[string]any{"type": "integer", "minimum": 0, "maximum": 1200, "description": "Wait up to this long for the reply (default 300)"},
		}, "required": []string{"task"}}, Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "openWorldHint": false}},
}

// mcpCallerUnknown is the service's answer while the calling conversation
// has not shown its tool call yet; the call is tried again shortly.
const mcpCallerUnknown = "Could not find the calling conversation"

// mcpRequest is a tool call as the service receives it.
type mcpRequest struct {
	TaskID    string `json:"taskId,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	// Tool is the miya tool being called, by which the service finds the
	// conversation calling it.
	Tool        string `json:"tool,omitempty"`
	WindowApp   string `json:"windowApp,omitempty"`
	WindowTitle string `json:"windowTitle,omitempty"`
	Thread      int    `json:"thread,omitempty"`
	Reference   string `json:"reference,omitempty"`
	Agent       string `json:"agent,omitempty"`
	Task        string `json:"task,omitempty"`
	NoContext   bool   `json:"noContext,omitempty"`
	// Arguments preserves a memory tool's original parameters for caller matching.
	Arguments json.RawMessage `json:"arguments,omitempty"`
	// Auto is an automation tool's arguments.
	Auto *mcpAutomationArgs `json:"auto,omitempty"`
	// Report is report_result\'s result.
	Report *automation.Report `json:"report,omitempty"`
}

type mcpThreadInfo struct {
	Reference string `json:"reference"`
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Agent     string `json:"agent"`
	Status    string `json:"status"`
}

type mcpThreadsResult struct {
	Threads []mcpThreadInfo `json:"threads"`
}

type mcpReadResult struct {
	Loaded bool   `json:"loaded"`
	Packet string `json:"packet"`
}

type mcpStatusResult struct {
	Reference string `json:"reference"`
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Agent     string `json:"agent"`
	Status    string `json:"status"`
	Reply     string `json:"reply"`
}

type mcpDelegateResult struct {
	TaskID    string `json:"taskId,omitempty"`
	Reference string `json:"reference"`
	Thread    int    `json:"thread"`
	Agent     string `json:"agent"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// runMCP serves MCP on stdio for the workspace named after -mcp, reaching
// the background service that is already running; it never starts one.
func runMCP() error {
	workspace, _ := os.Getwd()
	if len(os.Args) > 2 {
		workspace = os.Args[2]
	}
	// The service names its socket: agents may not pass MIYA_DIR on.
	sock := serviceSocket()
	if len(os.Args) > 3 {
		sock = os.Args[3]
	}
	var mu sync.Mutex
	var cl *serviceClient
	call := func(op string, args, out any) error {
		mu.Lock()
		if cl == nil {
			c, err := dialService(sock)
			if err != nil {
				mu.Unlock()
				return errors.New("Miya background service is not running. Open Miya first.")
			}
			cl = c
		}
		c := cl
		mu.Unlock()
		err := c.call(op, args, out)
		if errors.Is(err, errServiceClosed) {
			mu.Lock()
			cl = nil
			mu.Unlock()
		}
		return err
	}
	return serveMCP(os.Stdin, os.Stdout, workspace, call, time.Sleep)
}

type mcpServer struct {
	workspace string
	call      func(op string, args, out any) error
	sleep     func(time.Duration)
	ctx       context.Context // this MCP request; cancellation never cancels its task
}

// serveMCP answers newline-delimited JSON-RPC requests from r on w until r
// ends; requests run concurrently, as a delegation may wait for minutes.
func serveMCP(r io.Reader, w io.Writer, workspace string, call func(op string, args, out any) error, sleep func(time.Duration)) error {
	s := &mcpServer{workspace: workspace, call: call, sleep: sleep}
	ctx, cancelAll := context.WithCancel(context.Background())
	defer cancelAll()
	var pendingMu sync.Mutex
	pending := map[string]context.CancelFunc{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	enc := json.NewEncoder(w)
	write := func(v any) {
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(v)
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(line, &req) != nil {
			write(map[string]any{"jsonrpc": "2.0", "id": nil, "error": mcpError{Code: -32700, Message: "parse error"}})
			continue
		}
		if req.Method == "notifications/cancelled" {
			var params struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			if json.Unmarshal(req.Params, &params) == nil {
				pendingMu.Lock()
				if cancel := pending[string(bytes.TrimSpace(params.RequestID))]; cancel != nil {
					cancel()
				}
				pendingMu.Unlock()
			}
			continue
		}
		if len(req.ID) == 0 || req.Method == "" {
			continue // a notification, or a response to nothing we asked
		}
		key := string(req.ID)
		pendingMu.Lock()
		if _, exists := pending[key]; exists {
			pendingMu.Unlock()
			write(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": mcpError{Code: -32600, Message: "Request ID is already active"}})
			continue
		}
		requestCtx, cancelRequest := context.WithCancel(ctx)
		pending[key] = cancelRequest
		pendingMu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { cancelRequest(); pendingMu.Lock(); delete(pending, key); pendingMu.Unlock() }()
			requestServer := *s
			requestServer.ctx = requestCtx
			resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
			if result, err := requestServer.handle(req.Method, req.Params); err != nil {
				resp["error"] = err
			} else {
				resp["result"] = result
			}
			write(resp)
		}()
	}
	// EOF releases observers; accepted creation/mutation calls still finish
	// their durable work and are safe to retry with the original request ID.
	cancelAll()
	wg.Wait()
	return sc.Err()
}

func (s *mcpServer) observerError() error {
	if s.ctx != nil {
		return s.ctx.Err()
	}
	return nil
}

func (s *mcpServer) observerPause(delay time.Duration) error {
	if s.ctx == nil {
		s.sleep(delay)
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *mcpServer) handle(method string, params json.RawMessage) (any, *mcpError) {
	switch method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(params, &p)
		return map[string]any{
			"protocolVersion": textutil.FirstNonEmpty(p.ProtocolVersion, mcpProtocolVersion),
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "miya", "version": "1"},
			"instructions":    mcpInstructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(params, &p) != nil {
			return nil, &mcpError{Code: -32602, Message: "invalid params"}
		}
		text, err := s.callTool(p.Name, p.Arguments)
		if err != nil {
			return mcpToolResult(err.Error(), true), nil
		}
		return mcpToolResult(text, false), nil
	}
	return nil, &mcpError{Code: -32601, Message: "method not found: " + method}
}

func mcpToolResult(text string, isError bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isError}
}

func (s *mcpServer) callTool(name string, raw json.RawMessage) (string, error) {
	if coordinator.IsTool(name) {
		return s.collaborationTool(name, raw)
	}
	var a struct {
		WindowApp      string `json:"window_app"`
		WindowTitle    string `json:"window_title"`
		ID             string `json:"id"`
		Reason         string `json:"reason"`
		Task           string `json:"task"`
		Agent          string `json:"agent"`
		Thread         string `json:"thread_id"`
		IncludeContext *bool  `json:"include_context"`
		WaitSeconds    *int   `json:"wait_seconds"`
	}
	if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &a) != nil {
		return "", errors.New("Invalid arguments")
	}
	if name == "search_memory" || name == "remember" || name == "forget_memory" {
		return s.memoryTool(name, raw)
	}
	if strings.Contains(name, "automation") {
		return s.automationTool(name, raw)
	}
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	req := mcpRequest{Workspace: s.workspace, Tool: name, Arguments: raw}
	switch name {
	case "request_desktop_control":
		req.Task = a.Reason
		req.WindowApp, req.WindowTitle = a.WindowApp, a.WindowTitle
		var result string
		err := s.callService("mcpDesktopControl", req, &result)
		return result, err
	case "runtime_info":
		var result json.RawMessage
		err := s.callService("mcpRuntime", req, &result)
		return string(result), err
	case "report_result":
		var rep automation.Report
		if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &rep) != nil {
			return "", errors.New("Invalid arguments")
		}
		req.Report = &rep
		var text string
		err := s.callService("mcpReportResult", req, &text)
		return text, err
	case "list_threads":
		var res mcpThreadsResult
		if err := s.callService("mcpThreads", req, &res); err != nil {
			return "", err
		}
		return formatMCPThreads(res), nil
	case "read_thread":
		req.Reference = a.ID
		if _, ok := model.ParseThreadURL(req.Reference); !ok {
			return "", errors.New("id must be a full session link")
		}
		for i := 0; ; i++ {
			var res mcpReadResult
			if err := s.callService("mcpRead", req, &res); err != nil {
				return "", err
			}
			// An unloaded conversation loads in the service meanwhile.
			if res.Loaded {
				return res.Packet, nil
			}
			if i >= 20 {
				return "", errors.New("Failed to load session context. Try again later.")
			}
			s.sleep(500 * time.Millisecond)
		}
	case "thread_status":
		req.Reference = a.ID
		if _, ok := model.ParseThreadURL(req.Reference); !ok {
			return "", errors.New("id must be a full session link")
		}
		return s.status(req, mcpWait(a.WaitSeconds, 0))
	case "delegate":
		req.Task, req.Agent, req.Reference = a.Task, a.Agent, a.Thread
		if a.Thread != "" {
			if _, ok := model.ParseThreadURL(a.Thread); !ok {
				return "", errors.New("thread_id must be a full session link")
			}
		}
		req.NoContext = a.IncludeContext != nil && !*a.IncludeContext
		var res mcpDelegateResult
		if err := s.callService("mcpDelegate", req, &res); err != nil {
			return "", err
		}
		if res.TaskID != "" {
			req.TaskID = res.TaskID
			return s.legacyTaskStatus(req, mcpWait(a.WaitSeconds, 300))
		}
		out, err := s.status(mcpRequest{Workspace: s.workspace, Tool: name, Thread: res.Thread, Reference: res.Reference}, mcpWait(a.WaitSeconds, 300))
		return fmt.Sprintf("Handed off to session %s (%s).\n", textutil.FirstNonEmpty(res.Reference, "Creating"), backendFor(res.Agent).Name()) + out, err
	}
	return "", fmt.Errorf("Unknown tool %s", name)
}

// callService calls the service, again while it does not see the caller's
// tool call yet: the agent may report it just after making it.
func (s *mcpServer) callService(op string, args, out any) error {
	for i := 0; ; i++ {
		err := s.call(op, args, out)
		if err == nil || !strings.Contains(err.Error(), mcpCallerUnknown) || i >= 10 {
			return err
		}
		s.sleep(200 * time.Millisecond)
	}
}

// status reports a conversation, after waiting up to wait for it to finish.
func (s *mcpServer) status(req mcpRequest, wait time.Duration) (string, error) {
	const step = 2 * time.Second
	var res mcpStatusResult
	for waited := time.Duration(0); ; waited += step {
		res = mcpStatusResult{}
		if err := s.callService("mcpStatus", req, &res); err != nil {
			return "", err
		}
		if res.Reference != "" {
			req.Reference = res.Reference
		}
		if !subagent.Active(res.Status) || waited >= wait {
			break
		}
		s.sleep(step)
	}
	out := fmt.Sprintf("Conversation \"%s\" (%s): %s\n%s", res.Title, backendFor(res.Agent).Name(), subagent.StatusLabel(res.Status), res.Reference)
	if subagent.Active(res.Status) && wait > 0 {
		out += fmt.Sprintf(". Still running after %d seconds. Use thread_status to check later.", int(wait/time.Second))
	}
	if res.Reply != "" {
		out += "\n\nLatest response:\n" + res.Reply
	}
	return out, nil
}

func mcpWait(seconds *int, fallback int) time.Duration {
	v := fallback
	if seconds != nil {
		v = *seconds
	}
	return time.Duration(min(max(v, 0), 1200)) * time.Second
}

func formatMCPThreads(res mcpThreadsResult) string {
	if len(res.Threads) == 0 {
		return "This workspace has no other conversations in Miya."
	}
	var b strings.Builder
	for _, t := range res.Threads {
		status := "Idle"
		if t.Status == subagent.Running {
			status = "Running"
		}
		fmt.Fprintf(&b, "\"%s\"· %s · %s\n%s\n", t.Title, backendFor(t.Agent).Name(), status, t.Reference)
	}
	return strings.TrimRight(b.String(), "\n")
}
