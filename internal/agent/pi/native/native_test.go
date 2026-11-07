package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func clearProviderEnv(t *testing.T) {
	for _, k := range providerKeys {
		t.Setenv(k, "")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", t.TempDir())
}

func TestProviderFromClaudeLayersSettingsLikeClaudeCode(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "from-env")
	config := t.TempDir()
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://user.example","ANTHROPIC_MODEL":"claude-user"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(file, []byte(`{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:18888/","ANTHROPIC_AUTH_TOKEN":"proxy-token","OTHER":"ignored"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := ProviderFromClaude([]string{"--settings", file}, []string{"CLAUDE_CONFIG_DIR=" + config})
	if p.BaseURL != "http://127.0.0.1:18888" || p.Token != "proxy-token" || !p.Bearer || p.Model != "claude-user" || p.Source != file {
		t.Fatalf("provider = %+v", p)
	}
	if !p.Usable() {
		t.Fatal("provider should be usable")
	}

	// Inline settings and command variables override files.
	p = ProviderFromClaude([]string{"--settings", file, `--settings={"env":{"ANTHROPIC_MODEL":"claude-inline"}}`}, []string{"CLAUDE_CONFIG_DIR=" + config, "ANTHROPIC_BASE_URL=https://cmd.example"})
	if p.BaseURL != "https://cmd.example" || p.Model != "claude-inline" || p.Source != "launch command" {
		t.Fatalf("overridden provider = %+v", p)
	}
}

func TestProviderWithoutBaseURLIsNotUsable(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "key")
	if p := ProviderFromClaude(nil, nil); p.Usable() || p.Token != "key" || p.Bearer {
		t.Fatalf("provider = %+v", p)
	}
}

func TestLaunchKeepsTheTokenOutOfArguments(t *testing.T) {
	args, env := launch(Options{Args: []string{"--verbose"}, Provider: Provider{BaseURL: "http://proxy", Token: "secret", Bearer: true}, Extension: "/x/miya-provider.ts", Models: []string{"m1"}, Model: "m1", Thinking: "high", SessionFile: "/s/a.jsonl"})
	want := []string{"--verbose", "--mode", "rpc", "--extension", "/x/miya-provider.ts", "--provider", ProviderName, "--model", "m1", "--thinking", "high", "--session", "/s/a.jsonl"}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %q", args)
	}
	if strings.Contains(strings.Join(args, " "), "secret") {
		t.Fatal("token leaked into argv")
	}
	for _, kv := range []string{"MIYA_PI_API_KEY=secret", "MIYA_PI_BASE_URL=http://proxy", "MIYA_PI_BEARER=1", `MIYA_PI_MODELS=["m1"]`} {
		if !slices.Contains(env, kv) {
			t.Fatalf("env %q missing from %q", kv, env)
		}
	}

	// Without a usable provider pi keeps its own configuration.
	args, env = launch(Options{Extension: "/x/miya-provider.ts"})
	if slices.Contains(args, "--extension") || len(env) != 0 {
		t.Fatalf("args = %q env = %q", args, env)
	}
}

func TestExtensionHoldsNoCredentials(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	path, err := EnsureExtension()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "registerProvider") || !strings.Contains(string(data), `apiKey: "MIYA_PI_API_KEY"`) {
		t.Fatalf("extension = %q, %v", data, err)
	}
}

// TestHelperPi is a stand-in pi process for the client test.
func TestHelperPi(t *testing.T) {
	if os.Getenv("MIYA_FAKE_PI") != "1" {
		return
	}
	out := bufio.NewWriter(os.Stdout)
	emit := func(v map[string]any) {
		data, _ := json.Marshal(v)
		out.Write(append(data, '\n'))
		out.Flush()
	}
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		var cmd map[string]any
		_ = json.Unmarshal(in.Bytes(), &cmd)
		id, _ := cmd["id"].(string)
		switch cmd["type"] {
		case "get_state":
			emit(map[string]any{"type": "response", "id": id, "command": "get_state", "success": true, "data": map[string]any{"sessionId": "s-1", "sessionFile": "/tmp/s-1.jsonl"}})
		case "prompt":
			emit(map[string]any{"type": "response", "id": id, "command": "prompt", "success": true})
			emit(map[string]any{"type": "agent_start"})
			emit(map[string]any{"type": "extension_ui_request", "id": "ui-1", "method": "confirm", "title": "Allow?"})
			emit(map[string]any{"type": "message_update", "message": map[string]any{"role": "assistant"}, "assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "hi " + fmt.Sprint(cmd["message"])}})
		case "extension_ui_response":
			if cmd["cancelled"] == true {
				emit(map[string]any{"type": "agent_end", "messages": []any{map[string]any{"role": "assistant", "stopReason": "stop"}}})
			}
		default:
			emit(map[string]any{"type": "response", "id": id, "command": cmd["type"], "success": false, "error": "unknown"})
		}
	}
	os.Exit(0)
}

func TestClientRunsATurnOverRPC(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	var mu sync.Mutex
	var events []Event
	ended := make(chan struct{})
	done := make(chan error, 1)
	c, err := Start(context.Background(), Options{Binary: os.Args[0], Args: []string{"-test.run=^TestHelperPi$", "--"}, Env: []string{"MIYA_FAKE_PI=1"}}, func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
		if e.Type == "agent_end" {
			close(ended)
		}
	}, func(err error) { done <- err })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	state, err := c.State(ctx)
	if err != nil || state.SessionID != "s-1" || state.SessionFile != "/tmp/s-1.jsonl" {
		t.Fatalf("state = %+v, %v", state, err)
	}
	if err := c.Prompt(ctx, "there", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-ctx.Done():
		t.Fatal("the turn did not end: the extension dialog was not dismissed")
	}
	if c.Streaming() {
		t.Fatal("still streaming after agent_end")
	}
	if _, err := c.Call(ctx, map[string]any{"type": "bogus"}); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("failed command error = %v", err)
	}
	c.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stopped process reported %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("process did not end")
	}
	mu.Lock()
	defer mu.Unlock()
	var text string
	for _, e := range events {
		if e.Type == "message_update" {
			text += e.AssistantMessageEvent.Delta
		}
		if e.Type == "response" {
			t.Fatalf("a correlated response reached the event handler: %+v", e)
		}
	}
	if text != "hi there" {
		t.Fatalf("streamed text = %q", text)
	}
}

func TestInputSendsImagesInlineAndNamesOtherFiles(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "a.png")
	doc := filepath.Join(dir, "b.txt")
	_ = os.WriteFile(img, []byte("png"), 0o600)
	_ = os.WriteFile(doc, []byte("txt"), 0o600)
	text, images, err := Input("look", []string{img, doc})
	if err != nil || len(images) != 1 || images[0].MimeType != "image/png" || !strings.Contains(text, doc) || strings.Contains(text, img) {
		t.Fatalf("text = %q images = %+v err = %v", text, images, err)
	}
}
