// Package codex implements the native Codex app-server LF-delimited protocol.
package codex

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
	"sync"
	"time"

	"miya/internal/processenv"
)

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("Codex (%d): %s", e.Code, e.Message) }

type Frame struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}
type response struct {
	data json.RawMessage
	err  error
}
type Client struct {
	epoch       string
	cmd         *exec.Cmd
	input       io.WriteCloser
	writeMu     sync.Mutex
	mu          sync.Mutex
	pending     map[string]chan response
	next        uint64
	err         error
	done        chan struct{}
	events      chan Frame
	once        sync.Once
	exited      chan struct{}
	watchers    map[uint64]notificationWatcher
	nextWatcher uint64
}

type notificationWatcher struct {
	method string
	frames chan Frame
}

// WatchNotifications observes a native notification without taking it away
// from the controller's event stream. Register before starting an operation.
func (c *Client) WatchNotifications(method string) (<-chan Frame, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.watchers == nil {
		c.watchers = make(map[uint64]notificationWatcher)
	}
	c.nextWatcher++
	id := c.nextWatcher
	frames := make(chan Frame, 8)
	c.watchers[id] = notificationWatcher{method, frames}
	return frames, func() { c.mu.Lock(); delete(c.watchers, id); c.mu.Unlock() }
}

func Binary() (string, error) {
	if p := os.Getenv("CODEX_BINARY"); p != "" {
		return processenv.LookPath(p)
	}
	if p, err := processenv.LookPath("codex"); err == nil {
		return p, nil
	}
	const appBinary = "/Applications/Codex.app/Contents/Resources/codex"
	if info, err := os.Stat(appBinary); err == nil && !info.IsDir() {
		return appBinary, nil
	}
	return "", errors.New("Codex executable not found; install Codex CLI or set CODEX_BINARY")
}

// Start runs the app-server for cwd; args follow app-server, such as -c
// config overrides.
func Start(ctx context.Context, binary, cwd string, args ...string) (*Client, error) {
	return StartWithEnv(ctx, binary, cwd, os.Environ(), args...)
}

// StartWithEnv starts an isolated native operation with the CLI's account
// directory and toolchain environment supplied by its caller.
func StartWithEnv(ctx context.Context, binary, cwd string, env []string, args ...string) (*Client, error) {
	if !filepath.IsAbs(cwd) {
		return nil, errors.New("workspace must be an absolute path")
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("workspace is not a directory")
	}
	if binary == "" {
		binary, err = Binary()
		if err != nil {
			return nil, err
		}
	}
	cmd := exec.Command(binary, append([]string{"app-server", "--listen", "stdio://"}, args...)...)
	configureProcess(cmd)
	cmd.Dir = cwd
	// Authentication and provider configuration remain owned by Codex.
	cmd.Env = processenv.WithPath(env, binary)
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	stderr := &stderrTail{}
	cmd.Stderr = stderr
	// A background command Codex started can hold stderr open after Codex exits.
	cmd.WaitDelay = 2 * time.Second
	if err = cmd.Start(); err != nil {
		input.Close()
		output.Close()
		return nil, err
	}
	c := newClient(input)
	c.cmd = cmd
	c.epoch = fmt.Sprintf("%d:%d", cmd.Process.Pid, time.Now().UnixNano())
	c.exited = make(chan struct{})
	go func() {
		err := c.readFrames(output)
		waited := make(chan error, 1)
		go func() { waited <- cmd.Wait() }()
		var exitErr error
		exited := false
		if err != nil {
			// The stream usually ends because the process is exiting; its status
			// and stderr explain the end better than EOF does.
			select {
			case exitErr = <-waited:
				exited = true
			case <-time.After(time.Second):
			}
			if exitErr != nil {
				err = fmt.Errorf("Codex process exited: %w", exitErr)
			}
			c.fail(withStderr(err, stderr))
		}
		close(c.events)
		c.Close()
		if !exited {
			if exitErr = <-waited; exitErr != nil {
				c.fail(fmt.Errorf("Codex process exited: %w", exitErr))
			}
		}
		close(c.exited)
	}()
	if err = c.Call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "quickgui_chat", "title": "QuickGUI Chat", "version": "0.1.0"}, "capabilities": map[string]any{"experimentalApi": true}}, nil); err != nil {
		c.Close()
		select {
		case <-c.exited:
		case <-time.After(3 * time.Second):
		}
		return nil, withStderr(err, stderr)
	}
	if err = c.write(map[string]any{"method": "initialized"}); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}
func newClient(input io.WriteCloser) *Client {
	return &Client{input: input, pending: make(map[string]chan response), done: make(chan struct{}), events: make(chan Frame, 256)}
}
func (c *Client) Events() <-chan Frame  { return c.events }
func (c *Client) Done() <-chan struct{} { return c.done }
func (c *Client) Err() error            { c.mu.Lock(); defer c.mu.Unlock(); return c.err }
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.next++
	id := "quickgui:" + strconv.FormatUint(c.next, 10)
	key := strconv.Quote(id)
	ch := make(chan response, 1)
	c.pending[key] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, key); c.mu.Unlock() }()
	written := make(chan error, 1)
	go func() { written <- c.write(map[string]any{"id": id, "method": method, "params": params}) }()
	select {
	case err := <-written:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		c.Close()
		return fmt.Errorf("%s write: %w", method, ctx.Err())
	case <-c.done:
		return c.Err()
	}

	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if out != nil {
			return json.Unmarshal(r.data, out)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%s: %w", method, ctx.Err())
	case <-c.done:
		return c.Err()
	}
}
func (c *Client) Reply(id json.RawMessage, result any) error {
	return c.write(map[string]any{"id": id, "result": result})
}
func (c *Client) Reject(id json.RawMessage, message string) error {
	return c.write(map[string]any{"id": id, "error": RPCError{Code: -32601, Message: message}})
}
func (c *Client) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.done:
		return c.Err()
	default:
	}
	_, err = c.input.Write(data)
	if err != nil {
		c.fail(err)
	}
	return err
}
func (c *Client) read(r io.Reader) {
	if err := c.readFrames(r); err != nil {
		c.fail(err)
	}
	close(c.events)
	c.Close()
}

// readFrames delivers frames until the stream ends and says why it ended; nil
// means the client was closed first. Its caller fails the client before
// closing events, so a consumer that sees events end finds Err set.
func (c *Client) readFrames(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var f Frame
		if err := json.Unmarshal(scanner.Bytes(), &f); err != nil {
			return fmt.Errorf("invalid Codex protocol frame: %w", err)
		}
		if f.Method == "" && len(f.ID) > 0 {
			c.mu.Lock()
			ch := c.pending[string(f.ID)]
			delete(c.pending, string(f.ID))
			c.mu.Unlock()
			if ch != nil {
				var err error
				if f.Error != nil {
					err = f.Error
				}
				ch <- response{f.Result, err}
			}
			continue
		}
		if f.Method != "" {
			c.mu.Lock()
			for _, w := range c.watchers {
				if f.Method == w.method {
					select {
					case w.frames <- f:
					default:
					}
				}
			}
			c.mu.Unlock()
			select {
			case c.events <- f:
			case <-c.done:
				return nil
			}
		}
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	return fmt.Errorf("Codex connection closed: %w", err)
}
func (c *Client) fail(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.err = err
		c.pending = make(map[string]chan response)
		c.mu.Unlock()
		close(c.done)
	})
}
func (c *Client) Close() {
	c.fail(errors.New("Codex connection closed"))
	// Closing the pipe also releases a writer blocked by a dead server.
	c.input.Close()
	if c.cmd != nil {
		go func() {
			select {
			case <-c.exited:
				return
			case <-time.After(2 * time.Second):
			}
			// Ask first, so Codex can flush its rollout and stop its commands.
			interruptProcess(c.cmd)
			select {
			case <-c.exited:
			case <-time.After(3 * time.Second):
				terminateProcess(c.cmd)
			}
		}()
	}
}

// CloseAndWait is for owned short-lived processes. Their temporary account
// and working directories must outlive native shutdown and its final writes.
func (c *Client) CloseAndWait(ctx context.Context) error {
	c.Close()
	if c.exited == nil {
		return nil
	}
	select {
	case <-c.exited:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RuntimeEpoch identifies this app-server connection, shared by all its threads.
func (c *Client) RuntimeEpoch() string { return c.epoch }
