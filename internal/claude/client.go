// Package claude speaks Claude Code's newline-delimited stream-json protocol.
package claude

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
	"sync"
	"time"
)

func Binary() (string, error) {
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{filepath.Join(home, ".local/bin/claude"), "/opt/homebrew/bin/claude", "/usr/local/bin/claude"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0111 != 0 {
			return p, nil
		}
	}
	return "", errors.New("未找到 Claude Code，请先安装并登录 Claude Code CLI")
}

type Options struct {
	Binary, Cwd, SessionID, Model, PermissionMode, Effort string
	Resume                                                bool
	Tools                                                 *string
	Content                                               []map[string]any
	Args, Env                                             []string
}
type Frame struct {
	Type             string          `json:"type"`
	ToolUseResult    json.RawMessage `json:"tool_use_result"`
	CommandState     string          `json:"state"`
	WakeupSource     string          `json:"wakeup_source"`
	SchedulesKnown   bool            `json:"-"`
	Schedules        []ScheduledTask `json:"-"`
	KeepingAlive     bool            `json:"-"`
	WaitingForAgents bool            `json:"-"`
	AgentTasks       []AgentTask     `json:"-"`
	// Model is the model a system init frame reports.
	Model     string `json:"model"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	UUID      string `json:"uuid"`
	// ParentToolUseID is the Task call whose sub-agent sent the frame.
	ParentToolUseID string `json:"parent_tool_use_id"`
	RequestID       string `json:"request_id"`
	Request         struct {
		Subtype   string          `json:"subtype"`
		ToolName  string          `json:"tool_name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
	} `json:"request"`
	Response struct {
		Subtype   string `json:"subtype"`
		RequestID string `json:"request_id"`
		Error     string `json:"error"`
	} `json:"response"`
	Event   json.RawMessage `json:"event"`
	Message Message         `json:"message"`
	Result  string          `json:"result"`
	IsError bool            `json:"is_error"`
	Errors  []string        `json:"errors"`
	// Task frames (system task_started, task_progress, task_updated and
	// task_notification) report a sub-agent's state.
	TaskID      string          `json:"task_id"`
	ToolUseID   string          `json:"tool_use_id"`
	Description string          `json:"description"`
	Summary     string          `json:"summary"`
	Status      json.RawMessage `json:"status"`
	Usage       json.RawMessage `json:"usage"`
	Patch       json.RawMessage `json:"patch"`
}

// Message is a frame's message. Claude Code mostly sends an object whose
// content is a block list, but some frames carry a plain string message, and
// user messages may carry a plain string content; both decode to text blocks.
type Message struct {
	ID      string  `json:"id"`
	Content []Block `json:"content"`
}

func (m *Message) UnmarshalJSON(b []byte) error {
	*m = Message{}
	var s string
	if json.Unmarshal(b, &s) == nil {
		if s != "" {
			m.Content = []Block{{Type: "text", Text: s}}
		}
		return nil
	}
	var raw struct {
		ID      string          `json:"id"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	m.ID = raw.ID
	if len(raw.Content) == 0 || string(raw.Content) == "null" {
		return nil
	}
	if json.Unmarshal(raw.Content, &s) == nil {
		if s != "" {
			m.Content = []Block{{Type: "text", Text: s}}
		}
		return nil
	}
	return json.Unmarshal(raw.Content, &m.Content)
}

type Block struct {
	Type, ID, Name, Text string
	Input                json.RawMessage `json:"input"`
	ToolUseID            string          `json:"tool_use_id"`
	Content              json.RawMessage `json:"content"`
	IsError              bool            `json:"is_error"`
}
type Client struct {
	mu sync.Mutex
	// open is true while the turn takes more user messages: after the
	// first is written, until the result or the process ends.
	open                          bool
	idle                          bool
	stopping                      bool
	model, effort, permissionMode string
	controls                      map[string]chan error
	nextControl                   int
	in                            io.WriteCloser
	cancel                        context.CancelFunc
	done                          chan struct{}
}

func (c *Client) Write(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return json.NewEncoder(c.in).Encode(v)
}

// ErrNotSteerable tells that the turn takes no more messages now.
var ErrNotSteerable = errors.New("Claude Code 当前回合不接受追加消息")

func (c *Client) closeInput() {
	c.mu.Lock()
	c.open = false
	c.idle = false
	c.in.Close()
	c.mu.Unlock()
}

// Send reuses an idle process that owns background work. Starting a new
// process would discard its native schedules and background agents.
func (c *Client) Send(sessionID string, content []map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if (!c.idle && !c.open) || c.stopping {
		return ErrNotSteerable
	}
	if err := json.NewEncoder(c.in).Encode(map[string]any{"type": "user", "session_id": sessionID, "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": content}}); err != nil {
		return err
	}
	c.idle, c.open = false, true
	return nil
}

// SendWithOptions applies supported native controls before reusing a session.
// Effort is a launch setting in stream-json; refuse a change rather than
// silently running with the old value or discarding live session schedules.
func (c *Client) SendWithOptions(ctx context.Context, o Options) error {
	c.mu.Lock()
	model, mode, effort := c.model, c.permissionMode, c.effort
	c.mu.Unlock()
	if effort != o.Effort {
		return errors.New("请先停止循环，再更改推理强度并重新启动循环")
	}
	if mode != o.PermissionMode {
		if err := c.control(ctx, map[string]any{"subtype": "set_permission_mode", "mode": o.PermissionMode}); err != nil {
			return err
		}
		c.mu.Lock()
		c.permissionMode = o.PermissionMode
		c.mu.Unlock()
	}
	if model != o.Model {
		if err := c.control(ctx, map[string]any{"subtype": "set_model", "model": o.Model}); err != nil {
			return err
		}
		c.mu.Lock()
		c.model = o.Model
		c.mu.Unlock()
	}
	return c.Send(o.SessionID, o.Content)
}

func (c *Client) control(ctx context.Context, request map[string]any) error {
	c.mu.Lock()
	c.nextControl++
	id := fmt.Sprintf("settings-%d", c.nextControl)
	reply := make(chan error, 1)
	if c.controls == nil {
		c.controls = map[string]chan error{}
	}
	c.controls[id] = reply
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.controls, id); c.mu.Unlock() }()
	if err := c.Write(map[string]any{"type": "control_request", "request_id": id, "request": request}); err != nil {
		return err
	}
	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errors.New("Claude Code 会话已退出")
	}
}

// Steerable reports whether Steer would be written to the running turn.
func (c *Client) Steerable() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.open
}

// Steer writes a user message into the running turn, which Claude Code folds
// into it rather than starting another.
func (c *Client) Steer(sessionID string, content []map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.open {
		return ErrNotSteerable
	}
	return json.NewEncoder(c.in).Encode(map[string]any{"type": "user", "session_id": sessionID, "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": content}})
}

func (c *Client) Reply(id string, response any) error {
	return c.Write(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "response": response}})
}
func (c *Client) Stop() {
	c.mu.Lock()
	c.stopping, c.idle, c.open = true, false, false
	c.mu.Unlock()
	_ = c.Write(map[string]any{"type": "control_request", "request_id": "interrupt", "request": map[string]any{"subtype": "interrupt"}})
	go func() {
		select {
		case <-c.done:
		case <-time.After(2 * time.Second):
			c.cancel()
		}
	}()
}
func Start(parent context.Context, o Options, onFrame func(Frame), onDone func(error)) (*Client, error) {
	binary := o.Binary
	var err error
	if binary == "" {
		binary, err = Binary()
		if err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(parent)
	args := append(append([]string(nil), o.Args...), "--print", "--verbose", "--input-format", "stream-json", "--output-format", "stream-json", "--include-partial-messages", "--permission-prompt-tool", "stdio", "--permission-mode", o.PermissionMode)
	if o.Resume {
		args = append(args, "--resume", o.SessionID)
	} else if o.SessionID != "" {
		args = append(args, "--session-id", o.SessionID)
	}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Effort != "" {
		args = append(args, "--effort", o.Effort)
	}
	if o.Tools != nil {
		args = append(args, "--tools", *o.Tools)
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = o.Cwd
	if len(o.Env) > 0 {
		cmd.Env = append(os.Environ(), o.Env...)
	}
	configureProcess(cmd)
	cmd.WaitDelay = 2 * time.Second
	in, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		cancel()
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		in.Close()
		cancel()
		return nil, err
	}
	c := &Client{in: in, cancel: cancel, done: make(chan struct{}), model: o.Model, effort: o.Effort, permissionMode: o.PermissionMode}
	if err = c.Write(map[string]any{"type": "control_request", "request_id": "initialize", "request": map[string]any{"subtype": "initialize", "hooks": map[string]any{}}}); err != nil {
		cancel()
	}
	go func() {
		defer close(c.done)
		defer cancel()
		defer c.closeInput()
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 65536), 16*1024*1024)
		initialized, finished := false, false
		var schedules scheduleTracker
		var agents agentTracker
		var runErr error
		timer := time.AfterFunc(30*time.Second, func() { c.cancel() })
		defer timer.Stop()
		for scanner.Scan() {
			var f Frame
			if e := json.Unmarshal(scanner.Bytes(), &f); e != nil {
				runErr = fmt.Errorf("Claude Code 协议解析失败: %w", e)
				cancel()
				break
			}
			if f.Type == "control_response" && f.Response.RequestID != "initialize" {
				c.mu.Lock()
				reply := c.controls[f.Response.RequestID]
				c.mu.Unlock()
				if reply != nil {
					var err error
					if f.Response.Subtype == "error" {
						err = errors.New(f.Response.Error)
					}
					select {
					case reply <- err:
					default:
					}
					continue
				}
			}
			if f.Type == "control_response" && f.Response.RequestID == "initialize" {
				if f.Response.Subtype == "error" {
					runErr = errors.New(f.Response.Error)
					cancel()
					break
				}
				timer.Stop()
				initialized = true
				if e := c.Write(map[string]any{"type": "user", "session_id": o.SessionID, "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": o.Content}}); e != nil {
					runErr = e
					cancel()
					break
				}
				c.mu.Lock()
				c.open = true
				c.mu.Unlock()
				continue
			}
			schedules.apply(&f)
			agents.apply(&f)
			if f.ParentToolUseID == "" && (f.Type == "command_lifecycle" && f.CommandState == "started" || f.Type == "assistant" || f.Type == "stream_event") {
				finished = false
				c.mu.Lock()
				c.open, c.idle = !c.stopping, false
				c.mu.Unlock()
			}
			if f.Type == "result" && f.ParentToolUseID == "" {
				finished = true
				c.mu.Lock()
				keep := (schedules.active() || agents.active()) && !c.stopping && !f.IsError
				f.KeepingAlive = keep
				c.open, c.idle = false, keep
				c.mu.Unlock()
				if !keep {
					c.closeInput()
					// Allow Claude to flush its session before reaping background services.
					timer.Reset(5 * time.Second)
				}
			}
			onFrame(f)
		}
		if e := scanner.Err(); e != nil && runErr == nil {
			runErr = e
		}

		e := cmd.Wait()
		if runErr == nil && (!finished || agents.active()) {
			if !initialized {
				runErr = errors.New("Claude Code 初始化失败或超时，请检查 CLI 登录状态")
			} else if e != nil {
				runErr = e
			} else if agents.active() {
				runErr = errors.New("Claude Code 在子 agent 完成前退出")
			} else {
				runErr = errors.New("Claude Code 在返回结果前退出")
			}
		}
		onDone(runErr)
	}()
	return c, nil
}
