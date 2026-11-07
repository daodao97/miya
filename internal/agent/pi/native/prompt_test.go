package pi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSystemPromptArgs(t *testing.T) {
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	args, err := systemPromptArgs([]string{"--verbose"})
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 3 || args[0] != "--verbose" || args[1] != "--system-prompt" || !filepath.IsAbs(args[2]) {
		t.Fatalf("args = %q", args)
	}
	path := args[2]
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, systemPromptSource) {
		t.Fatalf("prompt file = %q, %v", data, err)
	}
	if err := os.WriteFile(path, []byte("outdated prompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := systemPromptArgs(nil); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(data, systemPromptSource) {
		t.Fatalf("prompt was not refreshed: %q, %v", data, err)
	}
	custom := []string{"--system-prompt", "User supplied prompt", "--append-system-prompt", "Extra instructions"}
	got, err := systemPromptArgs(custom)
	if err != nil || !slices.Equal(got, custom) {
		t.Fatalf("custom prompt changed: %q, %v", got, err)
	}
}

func TestSystemPromptPreparationFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MIYA_ISOLATED_DIR", path)
	if _, err := Start(context.Background(), Options{Binary: os.Args[0]}, nil, nil); err == nil || !strings.Contains(err.Error(), "prepare pi system prompt") {
		t.Fatalf("preparation error = %v", err)
	}
}

// This uses the real pi CLI against a local Anthropic-compatible server, so
// assertions inspect the actual provider request without paid model traffic.
func TestLiveSystemPromptOverride(t *testing.T) {
	binary := os.Getenv("MIYA_PI_LIVE_BINARY")
	if binary == "" {
		t.Skip("set MIYA_PI_LIVE_BINARY to test the real pi CLI")
	}
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	cwd := t.TempDir()
	for name, content := range map[string]string{
		"AGENTS.md":                        "MIYA_PROJECT_INSTRUCTIONS_PROBE",
		".pi/SYSTEM.md":                    "PROJECT_BASE_PROMPT_MUST_BE_REPLACED",
		".pi/APPEND_SYSTEM.md":             "MIYA_APPEND_PROBE",
		".pi/skills/prompt-probe/SKILL.md": "---\nname: prompt-probe\ndescription: MIYA_SKILL_DESCRIPTION_PROBE\n---\nTest skill.\n",
	} {
		path := filepath.Join(cwd, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	requests := make(chan json.RawMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			System json.RawMessage `json:"system"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		select {
		case requests <- body.System:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []struct{ name, data string }{
			{"message_start", `{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"miya-prompt-test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"MIYA_PI_OK"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`},
			{"message_stop", `{"type":"message_stop"}`},
		} {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.name, event.data)
		}
	}))
	defer server.Close()
	ext, err := EnsureExtension()
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan Event, 1)
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := Start(ctx, Options{
		Binary: binary, Cwd: cwd, Env: []string{"PI_CODING_AGENT_DIR=" + t.TempDir()},
		Provider:  Provider{BaseURL: server.URL, Token: "local-test-key"},
		Extension: ext, Models: []string{"miya-prompt-test"}, Model: "miya-prompt-test",
	}, func(e Event) {
		if e.Type == "agent_end" {
			ended <- e
		}
	}, func(err error) { done <- err })
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	if _, err := c.State(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Prompt(ctx, "Reply with MIYA_PI_OK", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-requests:
		var blocks []struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &blocks); err != nil {
			t.Fatalf("system blocks: %s, %v", raw, err)
		}
		var prompt strings.Builder
		for _, block := range blocks {
			prompt.WriteString(block.Text)
		}
		for _, want := range []string{string(systemPromptSource), "MIYA_PROJECT_INSTRUCTIONS_PROBE", "MIYA_SKILL_DESCRIPTION_PROBE", "MIYA_APPEND_PROBE", cwd} {
			if !strings.Contains(prompt.String(), want) {
				t.Errorf("provider system prompt missing %q", want)
			}
		}
		for _, unwanted := range []string{"PROJECT_BASE_PROMPT_MUST_BE_REPLACED", "You are an expert coding assistant operating inside pi", "Pi documentation ("} {
			if strings.Contains(prompt.String(), unwanted) {
				t.Errorf("provider system prompt still contains %q", unwanted)
			}
		}
	case err := <-done:
		t.Fatalf("pi exited before provider request: %v", err)
	case <-ctx.Done():
		t.Fatal("timed out waiting for provider request")
	}
	select {
	case e := <-ended:
		var reply strings.Builder
		for _, m := range e.Messages {
			if m.Role == "assistant" {
				reply.WriteString(Text(m.Blocks()))
			}
		}
		if reply.String() != "MIYA_PI_OK" {
			t.Fatalf("reply = %q", reply.String())
		}
		t.Log("real pi provider request uses Miya prompt; project context, skills, append prompt and cwd preserved; RPC turn completed")
	case err := <-done:
		t.Fatalf("pi exited before completing turn: %v", err)
	case <-ctx.Done():
		t.Fatal("timed out waiting for turn completion")
	}
}
