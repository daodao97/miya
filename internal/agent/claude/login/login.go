package login

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"miya/internal/agent/cli"
	"miya/internal/agent/login"
)

type Provider struct{ Binary, Home string }

func (p Provider) command(ctx context.Context, args ...string) (*exec.Cmd, error) {
	binary, home := p.Binary, p.Home
	var err error
	if binary == "" {
		binary, err = cli.FindBinary("claude")
	}
	if err != nil {
		return nil, errors.New("Claude Code not found. Install the official CLI first.")
	}
	if home == "" {
		home, err = cli.ConfigDir("claude")
	}
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env, cmd.Dir, cmd.Stderr = cli.CommandEnv("claude", home), os.TempDir(), io.Discard
	cmd.WaitDelay = time.Second
	cli.IsolateReader(cmd)
	return cmd, nil
}

func (p Provider) Check(ctx context.Context) (login.Identity, error) {
	cmd, err := p.command(ctx, "--setting-sources", "", "auth", "status", "--json")
	if err != nil {
		return login.Identity{}, err
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	err = cmd.Run()
	var status struct {
		LoggedIn     *bool  `json:"loggedIn"`
		AuthMethod   string `json:"authMethod"`
		Email        string `json:"email"`
		Subscription string `json:"subscriptionType"`
	}
	// The native status command exits 1 for a valid signed-out result.
	if json.Unmarshal(out.Bytes(), &status) != nil || status.LoggedIn == nil || ctx.Err() != nil {
		return login.Identity{}, errors.New("Unable to check Claude Code sign-in. Update the official CLI and try again.")
	}
	if err != nil && *status.LoggedIn {
		return login.Identity{}, errors.New("Unable to check Claude Code sign-in")
	}
	return login.Identity{LoggedIn: *status.LoggedIn, Method: status.AuthMethod, Email: status.Email, Plan: status.Subscription}, nil
}

// The official stream-json controls call the same OAuthService as the TUI.
// Unlike `auth login`, they accept a manual code and report token installation
// completion. A single owned process keeps the native PKCE state throughout.
func (p Provider) Start(ctx context.Context, method string) (login.Flow, login.Challenge, error) {
	cmd, err := p.command(ctx, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--setting-sources", "", "--tools", "", "--strict-mcp-config", "--no-session-persistence")
	if err != nil {
		return nil, login.Challenge{}, err
	}
	dir, err := os.MkdirTemp("", "miya-claude-login-")
	if err != nil {
		return nil, login.Challenge{}, err
	}
	cmd.Dir = dir
	f, err := start(cmd, dir)
	if err != nil {
		os.RemoveAll(dir)
		return nil, login.Challenge{}, err
	}
	var urls struct {
		ManualURL string `json:"manualUrl"`
	}
	startCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err = f.call(startCtx, "initialize", map[string]any{"subtype": "initialize"}, nil); err == nil {
		err = f.call(startCtx, "start", map[string]any{"subtype": "claude_authenticate", "loginWithClaudeAi": true}, &urls)
	}
	u, parseErr := url.Parse(urls.ManualURL)
	if err != nil || parseErr != nil || u.Scheme != "https" || (u.Host != "claude.ai" && u.Host != "claude.com") || u.User != nil || u.Query().Get("state") == "" || u.Query().Get("code_challenge") == "" || u.Query().Get("code") != "true" {
		f.Close()
		return nil, login.Challenge{}, errors.New("Unable to generate a native Claude sign-in link. Update the official CLI and try again.")
	}
	f.state = u.Query().Get("state")
	// Register the native completion waiter before exposing the manual URL.
	// A fast paste must not race with OAuthService clearing its completed flow.
	f.completion = make(chan reply, 1)
	f.mu.Lock()
	f.pending["wait"] = f.completion
	f.mu.Unlock()
	if err := f.write("wait", map[string]any{"subtype": "claude_oauth_wait_for_completion"}); err != nil {
		f.Close()
		return nil, login.Challenge{}, err
	}
	return f, login.Challenge{URL: urls.ManualURL}, nil
}

type reply struct {
	data   json.RawMessage
	failed bool
}
type flow struct {
	cmd        *exec.Cmd
	in         io.WriteCloser
	dir, state string
	mu         sync.Mutex
	writeMu    sync.Mutex
	pending    map[string]chan reply
	done       chan struct{}
	completion chan reply
	once       sync.Once
}

func start(cmd *exec.Cmd, dir string) (*flow, error) {
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		return nil, errors.New("Unable to start native Claude sign-in")
	}
	f := &flow{cmd: cmd, in: in, dir: dir, pending: make(map[string]chan reply), done: make(chan struct{})}
	go func() {
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			var frame struct {
				Type     string `json:"type"`
				Response struct {
					ID      string          `json:"request_id"`
					Subtype string          `json:"subtype"`
					Data    json.RawMessage `json:"response"`
				} `json:"response"`
			}
			if json.Unmarshal(scanner.Bytes(), &frame) != nil || frame.Type != "control_response" {
				continue
			}
			f.mu.Lock()
			ch := f.pending[frame.Response.ID]
			f.mu.Unlock()
			if ch != nil {
				select {
				case ch <- reply{frame.Response.Data, frame.Response.Subtype != "success"}:
				default:
				}
			}
		}
		_ = cmd.Wait()
		close(f.done)
	}()
	return f, nil
}

func (f *flow) call(ctx context.Context, id string, request any, out any) error {
	ch := make(chan reply, 1)
	f.mu.Lock()
	f.pending[id] = ch
	f.mu.Unlock()
	defer func() { f.mu.Lock(); delete(f.pending, id); f.mu.Unlock() }()
	if err := f.write(id, request); err != nil {
		return err
	}
	return f.await(ctx, ch, out)
}

func (f *flow) write(id string, request any) error {
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	if err := json.NewEncoder(f.in).Encode(map[string]any{"type": "control_request", "request_id": id, "request": request}); err != nil {
		return errors.New("Native Claude sign-in connection closed")
	}
	return nil
}

func (f *flow) await(ctx context.Context, ch <-chan reply, out any) error {
	select {
	case r := <-ch:
		if r.failed {
			return errors.New("Native Claude sign-in failed. Generate a new link and try again.")
		}
		if out != nil && json.Unmarshal(r.data, out) != nil {
			return errors.New("Unrecognized native Claude sign-in response")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-f.done:
		return errors.New("Native Claude sign-in process exited")
	}
}

func (f *flow) Submit(ctx context.Context, input string) error {
	code, state, ok := strings.Cut(input, "#")
	if !ok || code == "" || state != f.state || strings.ContainsAny(code, " \r\n\x00\x1b") {
		return errors.New("Paste the complete Claude authorization code, including #state, from this sign-in link")
	}
	return f.call(ctx, "callback", map[string]any{"subtype": "claude_oauth_callback", "authorizationCode": code, "state": state}, nil)
}

func (f *flow) Wait(ctx context.Context) error {
	return f.await(ctx, f.completion, nil)
}

func (f *flow) Close() {
	f.once.Do(func() {
		f.in.Close()
		if f.cmd.Cancel != nil {
			_ = f.cmd.Cancel()
		} else {
			_ = f.cmd.Process.Kill()
		}
		<-f.done
		os.RemoveAll(f.dir)
	})
}
