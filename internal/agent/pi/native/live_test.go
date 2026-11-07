package pi

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLiveTurnThroughClaudeProvider runs a real pi with Miya's extension and
// the provider of a Claude settings file:
//
//	MIYA_PI_LIVE_BINARY=$(which pi) MIYA_PI_LIVE_SETTINGS=~/client-settings.json go test -run Live ./internal/agent/pi/native/
func TestLiveTurnThroughClaudeProvider(t *testing.T) {
	binary, settings := os.Getenv("MIYA_PI_LIVE_BINARY"), os.Getenv("MIYA_PI_LIVE_SETTINGS")
	if binary == "" || settings == "" {
		t.Skip("set MIYA_PI_LIVE_BINARY and MIYA_PI_LIVE_SETTINGS")
	}
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	provider := ProviderFromClaude([]string{"--settings", settings}, nil)
	if !provider.Usable() {
		t.Fatalf("no usable provider in %s", settings)
	}
	ext, err := EnsureExtension()
	if err != nil {
		t.Fatal(err)
	}
	model := os.Getenv("MIYA_PI_LIVE_MODEL")
	if model == "" {
		model = "claude-sonnet-5-5"
	}
	thinking := os.Getenv("MIYA_PI_LIVE_THINKING")
	var mu sync.Mutex
	var text strings.Builder
	var stop string
	ended := make(chan struct{})
	done := make(chan error, 1)
	c, err := Start(context.Background(), Options{Binary: binary, Args: strings.Fields(os.Getenv("MIYA_PI_LIVE_ARGS")), Env: []string{"PI_CODING_AGENT_DIR=" + t.TempDir()}, Cwd: t.TempDir(), Provider: provider, Models: []string{model}, Extension: ext, Model: model, Thinking: thinking}, func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		switch e.Type {
		case "message_update":
			if e.AssistantMessageEvent.Type == "text_delta" {
				text.WriteString(e.AssistantMessageEvent.Delta)
			}
		case "message_end":
			if e.Message.Role == "assistant" {
				stop = e.Message.StopReason + " " + e.Message.ErrorMessage
			}
		case "agent_end":
			close(ended)
		}
	}, func(err error) { done <- err })
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	state, err := c.State(ctx)
	if err != nil {
		t.Fatalf("get_state: %v", err)
	}
	if state.Model == nil || state.Model.Provider != ProviderName || state.Model.ID != model {
		t.Fatalf("pi did not select the Claude provider: %+v", state.Model)
	}
	if err := c.Prompt(ctx, "Reply with exactly: MIYA_PI_OK", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case err := <-done:
		t.Fatalf("pi exited: %v", err)
	case <-ctx.Done():
		t.Fatal("turn timed out")
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(text.String(), "MIYA_PI_OK") {
		t.Fatalf("reply = %q (stop: %s)", text.String(), stop)
	}
	t.Logf("session %s, reply %q", state.SessionID, text.String())
}
