// Package claude speaks Claude Code's newline-delimited stream-json protocol.
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"miya/internal/processenv"
)

func Binary() (string, error) {
	if p, err := processenv.LookPath("claude"); err == nil {
		return p, nil
	}
	return "", errors.New("Claude Code not found. Install and sign in to the Claude Code CLI first.")
}

type Options struct {
	Binary, Cwd, SessionID, Model, PermissionMode, Effort string
	Resume                                                bool
	Tools                                                 *string
	Content                                               []map[string]any
	Args, Env                                             []string
	// BeforeInput checks the owner reservation after potentially slow native
	// initialization/configuration, immediately before submitting user input.
	BeforeInput func(context.Context) error
}
type NativeBackgroundTask struct {
	TaskID       string `json:"task_id"`
	RunID        string `json:"run_id"`
	TaskType     string `json:"task_type"`
	Description  string `json:"description"`
	ParentTaskID string `json:"parent_task_id"`
}

type Frame struct {
	TaskType         string                 `json:"task_type"`
	RunID            string                 `json:"run_id"`
	Tasks            []NativeBackgroundTask `json:"tasks"`
	BackgroundWork   bool                   `json:"-"`
	BackgroundTasks  []BackgroundTask       `json:"-"`
	Type             string                 `json:"type"`
	ToolUseResult    json.RawMessage        `json:"tool_use_result"`
	CommandState     string                 `json:"state"`
	WakeupSource     string                 `json:"wakeup_source"`
	SchedulesKnown   bool                   `json:"-"`
	Schedules        []ScheduledTask        `json:"-"`
	KeepingAlive     bool                   `json:"-"`
	WaitingForAgents bool                   `json:"-"`
	AgentTasks       []AgentTask            `json:"-"`
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
	Error   string          `json:"error"`
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
	ID      string          `json:"id"`
	Content []Block         `json:"content"`
	Usage   json.RawMessage `json:"usage"`
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
		Usage   json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	m.ID = raw.ID
	m.Usage = raw.Usage
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
	Type, ID, Name, Text, Thinking string
	Input                          json.RawMessage `json:"input"`
	ToolUseID                      string          `json:"tool_use_id"`
	Content                        json.RawMessage `json:"content"`
	IsError                        bool            `json:"is_error"`
}
type Client struct {
	pid             int
	epoch           string
	background      []BackgroundTask
	backgroundKnown bool
	mu              sync.Mutex
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
var ErrNotSteerable = errors.New("The current Claude Code turn cannot accept additional messages")

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
		return errors.New("Stop the loop before changing reasoning effort, then restart it")
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
	if o.BeforeInput != nil {
		if err := o.BeforeInput(ctx); err != nil {
			return err
		}
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
		return errors.New("The Claude Code session has exited")
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
	cmd.Env = processenv.Environ(binary, o.Env...)
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
	c := &Client{pid: cmd.Process.Pid, epoch: fmt.Sprintf("%d:%d", cmd.Process.Pid, time.Now().UnixNano()), in: in, cancel: cancel, done: make(chan struct{}), model: o.Model, effort: o.Effort, permissionMode: o.PermissionMode}
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
		var background backgroundTracker
		var runErr error
		timer := time.AfterFunc(30*time.Second, func() { c.cancel() })
		defer timer.Stop()
		idleTimer := time.AfterFunc(time.Hour, func() { c.closeIdleInput() })
		idleTimer.Stop()
		defer idleTimer.Stop()
		// A resumed CLI can finish restored background work before it starts
		// the submitted prompt. Keep an initial empty result provisional until
		// that prompt starts, the process exits, or the grace period expires.
		type scannedFrame struct {
			frame Frame
			err   error
		}
		frames := make(chan scannedFrame)
		go func() {
			defer close(frames)
			for scanner.Scan() {
				var f Frame
				err := json.Unmarshal(scanner.Bytes(), &f)
				if err != nil {
					err = fmt.Errorf("Failed to parse Claude Code protocol: %w", err)
				}
				select {
				case frames <- scannedFrame{f, err}:
				case <-ctx.Done():
					return
				}
				if err != nil {
					return
				}
			}
			if err := scanner.Err(); err != nil {
				select {
				case frames <- scannedFrame{err: err}:
				case <-ctx.Done():
				}
			}
		}()
		awaitingResumeTurn := o.Resume
		var pendingResult *Frame
		resumeTimer := time.NewTimer(time.Hour)
		resumeTimer.Stop()
		defer resumeTimer.Stop()
		var resumeDeadline <-chan time.Time
	readFrames:
		for {
			var f Frame
			finalizingEmptyResult := false
			select {
			case next, ok := <-frames:
				if !ok {
					if pendingResult == nil {
						break readFrames
					}
					f, finalizingEmptyResult = *pendingResult, true
					pendingResult, resumeDeadline = nil, nil
				} else if next.err != nil {
					runErr = next.err
					cancel()
					break readFrames
				} else {
					f = next.frame
				}
			case <-resumeDeadline:
				f, finalizingEmptyResult = *pendingResult, true
				pendingResult, resumeDeadline = nil, nil
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
				if o.BeforeInput != nil {
					if e := o.BeforeInput(ctx); e != nil {
						runErr = e
						cancel()
						break
					}
				}
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
			wasBackgroundActive := background.active()
			background.apply(&f)
			c.mu.Lock()
			c.background, c.backgroundKnown = background.snapshot(), background.known
			c.mu.Unlock()
			agents.apply(&f)
			c.mu.Lock()
			for _, agent := range agents.tasks {
				if agent.Status == "running" {
					c.background = append(c.background, BackgroundTask{ID: agent.ID, ToolUseID: agent.ToolUseID, Kind: "local_agent", Description: agent.Name, Status: agent.Status})
				}
			}
			c.mu.Unlock()
			if f.ParentToolUseID == "" && (f.Type == "command_lifecycle" && f.CommandState == "started" || f.Type == "assistant" || f.Type == "stream_event") {
				awaitingResumeTurn = false
				pendingResult, resumeDeadline = nil, nil
				resumeTimer.Stop()
				if initialized {
					timer.Stop()
				}
				finished = false
				idleTimer.Stop()
				c.mu.Lock()
				c.open, c.idle = !c.stopping, false
				c.mu.Unlock()
			}
			if f.Type == "result" && f.ParentToolUseID == "" {
				if !initialized {
					continue
				}
				c.mu.Lock()
				keep := (schedules.active() || agents.active() || background.active()) && !c.stopping && (!f.IsError || background.active())
				deferEmpty := awaitingResumeTurn && !keep && !c.stopping && !f.IsError && strings.TrimSpace(f.Result) == "" && !finalizingEmptyResult
				c.mu.Unlock()
				if deferEmpty {
					pendingResult = &f
					resumeTimer.Reset(5 * time.Second)
					resumeDeadline = resumeTimer.C
					continue
				}
				pendingResult, resumeDeadline = nil, nil
				resumeTimer.Stop()
				finished = true
				c.mu.Lock()
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
			if finished && wasBackgroundActive && !schedules.active() && !agents.active() && !background.active() {
				// Let native task notifications initiate their follow-up turn before
				// releasing an otherwise idle CLI. New output cancels this grace period.
				idleTimer.Reset(5 * time.Second)
			}
		}
		e := cmd.Wait()
		if runErr == nil && (!finished || agents.active() || background.active()) {
			if !initialized {
				runErr = errors.New("Claude Code initialization failed or timed out. Check CLI sign-in.")
			} else if e != nil {
				runErr = e
			} else if agents.active() {
				runErr = errors.New("Claude Code exited before the subagent finished")
			} else {
				runErr = errors.New("Claude Code exited before returning a result")
			}
		}
		onDone(runErr)
	}()
	return c, nil
}
