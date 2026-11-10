// Package pi runs the pi coding agent (github.com/badlogic/pi-mono) in its
// RPC mode: JSON commands on stdin, responses and agent events on stdout.
package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"miya/internal/processenv"
)

// ProviderName is the provider Miya's extension registers.
const ProviderName = "miya-claude"

type Options struct {
	Binary string
	// Args go before Miya's own options, Env is added to the environment.
	Args, Env []string
	Cwd       string
	// SessionFile resumes a session; empty starts a new one.
	SessionFile string
	// Provider, when usable, is registered by Miya's extension with Models.
	Provider  Provider
	Models    []string
	Extension string
	Model     string
	Thinking  string
}

// Block is one content block of a pi message.
type Block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// Message is pi's AgentMessage: user, assistant or toolResult.
type Message struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	Model        string          `json:"model,omitempty"`
	StopReason   string          `json:"stopReason,omitempty"`
	ErrorMessage string          `json:"errorMessage,omitempty"`
	ToolCallID   string          `json:"toolCallId,omitempty"`
	ToolName     string          `json:"toolName,omitempty"`
	IsError      bool            `json:"isError,omitempty"`
	Timestamp    int64           `json:"timestamp,omitempty"`
}

// Blocks are the message's content; a plain string is one text block.
func (m Message) Blocks() []Block { return Blocks(m.Content) }

func Blocks(raw json.RawMessage) []Block {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []Block{{Type: "text", Text: s}}
	}
	var out []Block
	_ = json.Unmarshal(raw, &out)
	return out
}

// Text joins the text blocks.
func Text(blocks []Block) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

type ToolResult struct {
	Content []Block `json:"content"`
}

// Event is any record pi writes: a command response or an agent event.
type Event struct {
	Type string `json:"type"`

	ID      string          `json:"id,omitempty"`
	Command string          `json:"command,omitempty"`
	Success bool            `json:"success,omitempty"`
	Error   string          `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`

	Message               Message   `json:"message"`
	Messages              []Message `json:"messages,omitempty"`
	AssistantMessageEvent struct {
		Type         string `json:"type"`
		ContentIndex int    `json:"contentIndex"`
		Delta        string `json:"delta"`
		Content      string `json:"content"`
		Reason       string `json:"reason"`
	} `json:"assistantMessageEvent"`

	ToolCallID    string          `json:"toolCallId,omitempty"`
	ToolName      string          `json:"toolName,omitempty"`
	Args          json.RawMessage `json:"args,omitempty"`
	Result        *ToolResult     `json:"result,omitempty"`
	PartialResult *ToolResult     `json:"partialResult,omitempty"`
	IsError       bool            `json:"isError,omitempty"`

	Method string `json:"method,omitempty"`

	ErrorMessage string `json:"errorMessage,omitempty"`
	FinalError   string `json:"finalError,omitempty"`
	Attempt      int    `json:"attempt,omitempty"`
	MaxAttempts  int    `json:"maxAttempts,omitempty"`
}

// State is get_state's answer.
type State struct {
	SessionFile string `json:"sessionFile"`
	SessionID   string `json:"sessionId"`
	SessionName string `json:"sessionName"`
	IsStreaming bool   `json:"isStreaming"`
	Model       *struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
	} `json:"model"`
}

type Image struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// Input is a prompt with its attachments: images are sent inline, other
// files are named so pi can read them with its tools.
func Input(text string, paths []string) (string, []Image, error) {
	var images []Image
	var files []string
	for _, p := range paths {
		typ := mime.TypeByExtension(filepath.Ext(p))
		if strings.HasPrefix(typ, "image/") {
			data, err := os.ReadFile(p)
			if err != nil {
				return "", nil, err
			}
			images = append(images, Image{Type: "image", Data: base64.StdEncoding.EncodeToString(data), MimeType: strings.Split(typ, ";")[0]})
			continue
		}
		files = append(files, p)
	}
	if len(files) > 0 {
		text += "\n\nAttached files:\n- " + strings.Join(files, "\n- ")
	}
	return text, images, nil
}

// Client is one running pi process.
type Client struct {
	pid       int
	epoch     string
	in        io.WriteCloser
	writeMu   sync.Mutex
	cancel    context.CancelFunc
	done      chan struct{}
	seq       atomic.Int64
	mu        sync.Mutex
	pending   map[string]chan Event
	streaming atomic.Bool
	stopped   atomic.Bool
}

// Start launches pi. onEvent receives every agent event on the reading
// goroutine; onDone receives the process' end once.
func Start(parent context.Context, o Options, onEvent func(Event), onDone func(error)) (*Client, error) {
	if o.Binary == "" {
		return nil, errors.New("Could not find pi command")
	}
	var err error
	o.Args, err = systemPromptArgs(o.Args)
	if err != nil {
		return nil, fmt.Errorf("prepare pi system prompt: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	args, env := launch(o)
	cmd := exec.CommandContext(ctx, o.Binary, args...)
	cmd.Dir = o.Cwd
	cmd.Env = processenv.Environ(o.Binary, env...)
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
	stderr := &tail{max: 4096}
	cmd.Stderr = stderr
	if err = cmd.Start(); err != nil {
		in.Close()
		cancel()
		return nil, err
	}
	c := &Client{pid: cmd.Process.Pid, epoch: fmt.Sprintf("pi:%d:%d", cmd.Process.Pid, time.Now().UnixNano()), in: in, cancel: cancel, done: make(chan struct{}), pending: map[string]chan Event{}}
	go func() {
		defer close(c.done)
		defer cancel()
		r := bufio.NewReaderSize(out, 64*1024)
		for {
			line, err := r.ReadBytes('\n')
			line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
			if len(bytes.TrimSpace(line)) > 0 {
				c.receive(line, onEvent)
			}
			if err != nil {
				break
			}
		}
		waitErr := cmd.Wait()
		c.failPending()
		if c.stopped.Load() {
			waitErr = nil
		} else if waitErr != nil || stderr.String() != "" && !c.streaming.Load() {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				waitErr = errors.New("pi: " + lastLines(msg, 6))
			} else if waitErr != nil {
				waitErr = fmt.Errorf("pi exited: %w", waitErr)
			}
		}
		if onDone != nil {
			onDone(waitErr)
		}
	}()
	return c, nil
}

// launch is pi's command line and the variables Miya adds for its extension.
func launch(o Options) (args, env []string) {
	args = append(append([]string(nil), o.Args...), "--mode", "rpc")
	env = append([]string(nil), o.Env...)
	if o.Provider.Usable() && o.Extension != "" {
		models, _ := json.Marshal(o.Models)
		args = append(args, "--extension", o.Extension, "--provider", ProviderName)
		env = append(env, "MIYA_PI_PROVIDER="+ProviderName, "MIYA_PI_BASE_URL="+o.Provider.BaseURL, "MIYA_PI_API_KEY="+o.Provider.Token, "MIYA_PI_MODELS="+string(models))
		if o.Provider.Bearer {
			env = append(env, "MIYA_PI_BEARER=1")
		}
	}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Thinking != "" {
		args = append(args, "--thinking", o.Thinking)
	}
	if o.SessionFile != "" {
		args = append(args, "--session", o.SessionFile)
	}
	return args, env
}

func (c *Client) receive(line []byte, onEvent func(Event)) {
	var e Event
	if json.Unmarshal(line, &e) != nil {
		return
	}
	switch e.Type {
	case "response":
		c.mu.Lock()
		ch := c.pending[e.ID]
		delete(c.pending, e.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- e
			return
		}
	case "agent_start":
		c.streaming.Store(true)
	case "agent_end":
		c.streaming.Store(false)
	case "extension_ui_request":
		// Miya has no dialogs for extensions yet: dismiss them rather than
		// leave the agent waiting on one nobody sees.
		switch e.Method {
		case "select", "confirm", "input", "editor":
			go c.write(map[string]any{"type": "extension_ui_response", "id": e.ID, "cancelled": true})
		}
	}
	if onEvent != nil {
		onEvent(e)
	}
}

func (c *Client) failPending() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

func (c *Client) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.in.Write(append(data, '\n'))
	return err
}

// Call sends a command and waits for its response.
func (c *Client) Call(ctx context.Context, cmd map[string]any) (Event, error) {
	id := fmt.Sprintf("miya-%d", c.seq.Add(1))
	ch := make(chan Event, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	cmd["id"] = id
	if err := c.write(cmd); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return Event{}, err
	}
	select {
	case e, ok := <-ch:
		if !ok {
			return Event{}, errors.New("pi exited")
		}
		if !e.Success {
			return e, errors.New("pi: " + firstNonEmpty(e.Error, "command "+e.Command+" failed"))
		}
		return e, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return Event{}, ctx.Err()
	case <-c.done:
		return Event{}, errors.New("pi exited")
	}
}

func (c *Client) State(ctx context.Context) (State, error) {
	e, err := c.Call(ctx, map[string]any{"type": "get_state"})
	var s State
	if err == nil {
		err = json.Unmarshal(e.Data, &s)
	}
	return s, err
}

// Prompt starts a turn, or while one runs, steers it.
func (c *Client) Prompt(ctx context.Context, text string, images []Image) error {
	cmd := map[string]any{"type": "prompt", "message": text}
	if len(images) > 0 {
		cmd["images"] = images
	}
	if c.streaming.Load() {
		cmd["streamingBehavior"] = "steer"
	}
	_, err := c.Call(ctx, cmd)
	return err
}

func (c *Client) SetModel(ctx context.Context, provider, model string) error {
	_, err := c.Call(ctx, map[string]any{"type": "set_model", "provider": provider, "modelId": model})
	return err
}

func (c *Client) SetThinking(ctx context.Context, level string) error {
	_, err := c.Call(ctx, map[string]any{"type": "set_thinking_level", "level": level})
	return err
}

// Abort stops the running turn and keeps the process for the next one.
func (c *Client) Abort() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.Call(ctx, map[string]any{"type": "abort"})
	return err
}

// Stop ends the process.
func (c *Client) Stop() {
	c.stopped.Store(true)
	c.writeMu.Lock()
	_ = c.in.Close()
	c.writeMu.Unlock()
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		c.cancel()
	}
}

func (c *Client) Streaming() bool       { return c.streaming.Load() }
func (c *Client) Done() <-chan struct{} { return c.done }
func (c *Client) PID() int              { return c.pid }
func (c *Client) Epoch() string         { return c.epoch }
func (c *Client) Alive() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

// tail keeps the end of what a process writes.
type tail struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
