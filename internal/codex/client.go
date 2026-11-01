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
	"strings"
	"sync"
	"time"
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
	cmd     *exec.Cmd
	input   io.WriteCloser
	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan response
	next    uint64
	err     error
	done    chan struct{}
	events  chan Frame
	once    sync.Once
	exited  chan struct{}
}

func Binary() (string, error) {
	if p := os.Getenv("CODEX_BINARY"); p != "" {
		return exec.LookPath(p)
	}
	if p, err := exec.LookPath("codex"); err == nil {
		return p, nil
	}
	for _, p := range []string{"/opt/homebrew/bin/codex", "/usr/local/bin/codex", "/Applications/Codex.app/Contents/Resources/codex"} {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("Codex executable not found; install Codex CLI or set CODEX_BINARY")
}

// Packaged macOS apps inherit a minimal Finder PATH. The Homebrew Codex entry
// point is a Node script, so make the common Node/Homebrew locations visible to
// the child process even when the app was launched by Finder rather than a
// shell.
func withToolchainPath(env []string) []string {
	const pathKey = "PATH="
	var current string
	for _, item := range env {
		if strings.HasPrefix(item, pathKey) {
			current = strings.TrimPrefix(item, pathKey)
			break
		}
	}
	parts := strings.Split(current, string(os.PathListSeparator))
	seen := make(map[string]bool, len(parts)+4)
	for _, part := range parts {
		if part != "" {
			seen[part] = true
		}
	}
	for _, dir := range []string{"/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin", "/usr/local/sbin"} {
		if !seen[dir] {
			parts = append([]string{dir}, parts...)
			seen[dir] = true
		}
	}
	value := pathKey + strings.Join(parts, string(os.PathListSeparator))
	for i, item := range env {
		if strings.HasPrefix(item, pathKey) {
			out := append([]string(nil), env...)
			out[i] = value
			return out
		}
	}
	return append(append([]string(nil), env...), value)
}

// Start runs the app-server for cwd; args follow app-server, such as -c
// config overrides.
func Start(ctx context.Context, binary, cwd string, args ...string) (*Client, error) {
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
	cmd.Env = withToolchainPath(os.Environ())
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	cmd.Stderr = io.Discard // Never put auth/config contents in UI logs.
	if err = cmd.Start(); err != nil {
		input.Close()
		output.Close()
		return nil, err
	}
	c := newClient(input)
	c.cmd = cmd
	c.exited = make(chan struct{})
	go func() {
		c.read(output)
		err := cmd.Wait()
		if err != nil {
			c.fail(fmt.Errorf("Codex process exited: %w", err))
		}
		close(c.exited)
	}()
	if err = c.Call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "quickgui_chat", "title": "QuickGUI Chat", "version": "0.1.0"}}, nil); err != nil {
		c.Close()
		return nil, err
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
	defer c.Close()
	defer close(c.events)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var f Frame
		if err := json.Unmarshal(scanner.Bytes(), &f); err != nil {
			c.fail(fmt.Errorf("invalid Codex protocol frame: %w", err))
			return
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
			select {
			case c.events <- f:
			case <-c.done:
				return
			}
		}
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	c.fail(fmt.Errorf("Codex connection closed: %w", err))
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
				terminateProcess(c.cmd)
			}
		}()
	}
}
