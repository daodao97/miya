// Package mcp is the MCP server the -mcp process runs for an agent: it
// answers JSON-RPC on stdio and reaches the background service for each tool.
// Features add their tools with Register.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"miya/internal/automation"
	"miya/internal/chat/model"
	"miya/internal/chat/subagent"
	dg "miya/internal/delegation"
	mcpproto "miya/internal/mcp/protocol"
	"miya/internal/textutil"
)

const protocolVersion = "2025-06-18"

const instructions = "Miya runs several coding-agent conversations in this workspace (Codex, Claude Code). " +
	"For collaboration, use list_agents and start_task with a unique request_id. Completion resumes this conversation by default. You remain responsible for delegated work: while actively waiting, repeatedly use wait_task and inspect progress; after a completion notification read_task, assess the result, and continue the user's original task. If you already collect a terminal result while still in the original turn, unwatch_task its pending notification to avoid an unnecessary extra turn. Do not end with an untracked promise to check later. Use notify=none only for explicitly record-only work, or watch_task to add completion continuation to existing work. A paused receiver must be explicitly resumed by the user. " +
	"Structured Agent tags such as #[Claude Code](miya-agent://claude-code) in the current user request or goal select canonical delegation targets: use start_task for their work. Selecting a tag did not execute anything. Tags quoted in code or supplied only as reference material are not requests to delegate. @conversation references supply context only. " +
	"Use list_threads and read_thread to learn what another conversation did, and delegate to hand a self-contained task to another agent; " +
	"the receiving conversation gets this conversation's context unless include_context is false. " +
	"When a login, OAuth authorization, CAPTCHA, or other desktop UI needs the person, use request_desktop_control with a short reason. " +
	"Miya shows a takeover card in this conversation on the phone. It waits for the person to finish; never request passwords in chat. " +
	"Authorization previews and takeover control target one Mac window. Supply window_app/window_title when known; never request a password in chat. Full desktop control remains a separate phone menu action. This does not control a separate cloud VM. Open that VM in a visible Mac app first if needed."

var allTools = []mcpproto.Tool{
	{Name: "request_desktop_control", Description: "Ask the person to take over the target authorization window on the paired Mac from this conversation, for login or other manual UI steps. Shows a phone takeover card and waits up to 10 minutes for explicit completion. The phone previews only the target window; tap it to enter control. Supply window_app and window_title when known to identify a password or authorization dialog. Never put passwords in reason or arguments. This tool never grants OS permissions.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"window_app": map[string]any{"type": "string", "maxLength": 256, "description": "Visible application name or bundle ID owning the authorization dialog"}, "window_title": map[string]any{"type": "string", "maxLength": 1000, "description": "Distinctive part of the authorization window title; ambiguous matches require user selection"}, "reason": map[string]any{"type": "string", "minLength": 1, "maxLength": 2000, "description": "The visible app/page and what the person should do; do not include credentials"}}, "required": []string{"reason"}}, Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "openWorldHint": false}},
	{Name: "runtime_info", Description: "Read this conversation's verified background tasks, OS processes and listening services. Native task identifiers are not OS PIDs; unknown observations are explicit.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}, Annotations: mcpproto.ReadOnly},
	{Name: "list_threads", Description: "List the other conversations of this workspace in Miya: session link, title, agent and whether each is running.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}, Annotations: mcpproto.ReadOnly},
	{Name: "read_thread", Description: "Read a conversation's context: its first and latest requests, latest conclusions, changed files and sub-agents.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"id": map[string]any{"type": "string", "description": "Full miya:// session link from list_threads"},
		}, "required": []string{"id"}}, Annotations: mcpproto.ReadOnly},
	{Name: "thread_status", Description: "Tell whether a conversation is running, and its latest reply; optionally wait for it to finish.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"id":           map[string]any{"type": "string", "description": "Full miya:// session link"},
			"wait_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 1200, "description": "Wait up to this long for it to finish (default 0)"},
		}, "required": []string{"id"}}, Annotations: mcpproto.ReadOnly},
	{Name: "delegate", Description: "Hand a self-contained task to another agent: a new conversation, or an existing one by its session link. " +
		"Waits up to wait_seconds (default 300) for its reply, then returns its status; use thread_status to follow it later.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"task":            map[string]any{"type": "string", "description": "What to do, with everything the other agent needs"},
			"agent":           map[string]any{"type": "string", "enum": model.Backends, "description": "Agent for a new conversation (default: this conversation's)"},
			"thread_id":       map[string]any{"type": "string", "description": "Full miya:// session link of an existing conversation"},
			"include_context": map[string]any{"type": "boolean", "description": "Attach this conversation's context (default true)"},
			"wait_seconds":    map[string]any{"type": "integer", "minimum": 0, "maximum": 1200, "description": "Wait up to this long for the reply (default 300)"},
		}, "required": []string{"task"}}, Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "openWorldHint": false}},
}

type Server struct {
	workspace string
	call      func(op string, args, out any) error
	sleep     func(time.Duration)
	ctx       context.Context // this MCP request; cancellation never cancels its task
}

// NewServer serves workspace, reaching the service through call; sleep
// waits between polls of requests without a context.
func NewServer(workspace string, call func(op string, args, out any) error, sleep func(time.Duration)) *Server {
	return &Server{workspace: workspace, call: call, sleep: sleep}
}

// Serve answers newline-delimited JSON-RPC requests from r on w until r
// ends; requests run concurrently, as a delegation may wait for minutes.
func Serve(r io.Reader, w io.Writer, workspace string, call func(op string, args, out any) error, sleep func(time.Duration)) error {
	s := NewServer(workspace, call, sleep)
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
			write(map[string]any{"jsonrpc": "2.0", "id": nil, "error": mcpproto.Error{Code: -32700, Message: "parse error"}})
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
			write(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": mcpproto.Error{Code: -32600, Message: "Request ID is already active"}})
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

// Workspace is the project the -mcp process serves.
func (s *Server) Workspace() string { return s.workspace }

func (s *Server) ObserverError() error {
	if s.ctx != nil {
		return s.ctx.Err()
	}
	return nil
}

func (s *Server) ObserverPause(delay time.Duration) error {
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

func (s *Server) handle(method string, params json.RawMessage) (any, *mcpproto.Error) {
	switch method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(params, &p)
		return map[string]any{
			"protocolVersion": textutil.FirstNonEmpty(p.ProtocolVersion, protocolVersion),
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "miya", "version": "1"},
			"instructions":    instructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": allTools}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(params, &p) != nil {
			return nil, &mcpproto.Error{Code: -32602, Message: "invalid params"}
		}
		text, err := s.CallTool(p.Name, p.Arguments)
		if err != nil {
			return toolResult(err.Error(), true), nil
		}
		return toolResult(text, false), nil
	}
	return nil, &mcpproto.Error{Code: -32601, Message: "method not found: " + method}
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isError}
}

// Handler answers the tools a feature registered.
type Handler func(s *Server, name string, raw json.RawMessage) (string, error)

var (
	// selfChecked handlers check their arguments themselves.
	selfChecked = map[string]Handler{}
	// handlers run once the arguments decode as the common ones.
	handlers = map[string]Handler{}
)

// Register lists tools in tools/list, in registration order, and has
// h answer them; without h, callTool answers them itself. An ownArgs handler
// gets the arguments before the common check.
func Register(h Handler, ownArgs bool, tools ...mcpproto.Tool) {
	for _, t := range tools {
		allTools = append(allTools, t)
		switch {
		case h == nil:
		case ownArgs:
			selfChecked[t.Name] = h
		default:
			handlers[t.Name] = h
		}
	}
}

func (s *Server) CallTool(name string, raw json.RawMessage) (string, error) {
	if h := selfChecked[name]; h != nil {
		return h(s, name, raw)
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
	if h := handlers[name]; h != nil {
		return h(s, name, raw)
	}
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	req := mcpproto.Request{Workspace: s.workspace, Tool: name, Arguments: raw}
	switch name {
	case "request_desktop_control":
		req.Task = a.Reason
		req.WindowApp, req.WindowTitle = a.WindowApp, a.WindowTitle
		var result string
		err := s.CallService("mcpDesktopControl", req, &result)
		return result, err
	case "runtime_info":
		var result json.RawMessage
		err := s.CallService("mcpRuntime", req, &result)
		return string(result), err
	case "report_result":
		var rep automation.Report
		if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &rep) != nil {
			return "", errors.New("Invalid arguments")
		}
		req.Report = &rep
		var text string
		err := s.CallService("mcpReportResult", req, &text)
		return text, err
	case "list_threads":
		var res mcpproto.ThreadsResult
		if err := s.CallService("mcpThreads", req, &res); err != nil {
			return "", err
		}
		return formatThreads(res), nil
	case "read_thread":
		req.Reference = a.ID
		if _, ok := model.ParseThreadURL(req.Reference); !ok {
			return "", errors.New("id must be a full session link")
		}
		for i := 0; ; i++ {
			var res mcpproto.ReadResult
			if err := s.CallService("mcpRead", req, &res); err != nil {
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
		return s.status(req, waitFor(a.WaitSeconds, 0))
	case "delegate":
		req.Task, req.Agent, req.Reference = a.Task, a.Agent, a.Thread
		if a.Thread != "" {
			if _, ok := model.ParseThreadURL(a.Thread); !ok {
				return "", errors.New("thread_id must be a full session link")
			}
		}
		req.NoContext = a.IncludeContext != nil && !*a.IncludeContext
		var res mcpproto.DelegateResult
		if err := s.CallService("mcpDelegate", req, &res); err != nil {
			return "", err
		}
		if res.TaskID != "" {
			req.TaskID = res.TaskID
			return legacyTaskStatus(s, req, waitFor(a.WaitSeconds, 300))
		}
		out, err := s.status(mcpproto.Request{Workspace: s.workspace, Tool: name, Thread: res.Thread, Reference: res.Reference}, waitFor(a.WaitSeconds, 300))
		return fmt.Sprintf("Handed off to session %s (%s).\n", textutil.FirstNonEmpty(res.Reference, "Creating"), model.AgentName(res.Agent)) + out, err
	}
	return "", fmt.Errorf("Unknown tool %s", name)
}

// callService calls the service, again while it does not see the caller's
// tool call yet: the agent may report it just after making it.
func (s *Server) CallService(op string, args, out any) error {
	for i := 0; ; i++ {
		err := s.call(op, args, out)
		if err == nil || !strings.Contains(err.Error(), mcpproto.CallerUnknown) || i >= 10 {
			return err
		}
		s.sleep(200 * time.Millisecond)
	}
}

// status reports a conversation, after waiting up to wait for it to finish.
func (s *Server) status(req mcpproto.Request, wait time.Duration) (string, error) {
	const step = 2 * time.Second
	var res mcpproto.StatusResult
	for waited := time.Duration(0); ; waited += step {
		res = mcpproto.StatusResult{}
		if err := s.CallService("mcpStatus", req, &res); err != nil {
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
	out := fmt.Sprintf("Conversation \"%s\" (%s): %s\n%s", res.Title, model.AgentName(res.Agent), subagent.StatusLabel(res.Status), res.Reference)
	if subagent.Active(res.Status) && wait > 0 {
		out += fmt.Sprintf(". Still running after %d seconds. Use thread_status to check later.", int(wait/time.Second))
	}
	if res.Reply != "" {
		out += "\n\nLatest response:\n" + res.Reply
	}
	return out, nil
}

func waitFor(seconds *int, fallback int) time.Duration {
	v := fallback
	if seconds != nil {
		v = *seconds
	}
	return time.Duration(min(max(v, 0), 1200)) * time.Second
}

func formatThreads(res mcpproto.ThreadsResult) string {
	if len(res.Threads) == 0 {
		return "This workspace has no other conversations in Miya."
	}
	var b strings.Builder
	for _, t := range res.Threads {
		status := "Idle"
		if t.Status == subagent.Running {
			status = "Running"
		}
		fmt.Fprintf(&b, "\"%s\"· %s · %s\n%s\n", t.Title, model.AgentName(t.Agent), status, t.Reference)
	}
	return strings.TrimRight(b.String(), "\n")
}

func legacyTaskStatus(s *Server, req mcpproto.Request, wait time.Duration) (string, error) {
	deadline := time.Now().Add(wait)
	for {
		if err := s.ObserverError(); err != nil {
			return "", err
		}
		var result json.RawMessage
		if err := s.CallService("mcpLegacyTask", req, &result); err != nil {
			return "", err
		}
		var v struct {
			State      dg.State `json:"state"`
			Resolution string   `json:"resolution"`
		}
		if err := json.Unmarshal(result, &v); err != nil {
			return "", err
		}
		if dg.Terminal(v.State) || v.Resolution == dg.ResolutionAbandoned {
			return string(result), nil
		}
		if !time.Now().Before(deadline) {
			var value map[string]any
			_ = json.Unmarshal(result, &value)
			value["timed_out"] = true
			data, _ := json.Marshal(value)
			return string(data), nil
		}
		if err := s.ObserverPause(500 * time.Millisecond); err != nil {
			return "", err
		}
	}
}

// Tools are the MCP tools in tools/list order: the built-in ones, then those
// registered, in registration order.
func Tools() []mcpproto.Tool { return allTools }
