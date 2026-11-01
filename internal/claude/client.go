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
	Type string `json:"type"`
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
	Message struct {
		ID      string  `json:"id"`
		Content []Block `json:"content"`
	} `json:"message"`
	Result  string   `json:"result"`
	IsError bool     `json:"is_error"`
	Errors  []string `json:"errors"`
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
	open   bool
	in     io.WriteCloser
	cancel context.CancelFunc
	done   chan struct{}
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
	c.in.Close()
	c.mu.Unlock()
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
	c := &Client{in: in, cancel: cancel, done: make(chan struct{})}
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
			onFrame(f)
			if f.Type == "result" {
				finished = true
				c.closeInput()
				// Allow Claude to flush its session before reaping background services.
				timer.Reset(5 * time.Second)
			}
		}
		if e := scanner.Err(); e != nil && runErr == nil {
			runErr = e
		}

		e := cmd.Wait()
		if runErr == nil && !finished {
			if !initialized {
				runErr = errors.New("Claude Code 初始化失败或超时，请检查 CLI 登录状态")
			} else if e != nil {
				runErr = e
			} else {
				runErr = errors.New("Claude Code 在返回结果前退出")
			}
		}
		onDone(runErr)
	}()
	return c, nil
}
