package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func init() {
	if os.Getenv("QUICKGUI_CLAUDE_TEST_HELPER") != "1" {
		return
	}
	d := json.NewDecoder(os.Stdin)
	var v map[string]any
	if d.Decode(&v) != nil || v["type"] != "control_request" {
		os.Exit(21)
	}
	fmt.Println(`{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}`)
	if d.Decode(&v) != nil || v["type"] != "user" {
		os.Exit(22)
	}
	fmt.Println(`{"type":"system","subtype":"init","session_id":"session-test"}`)
	fmt.Println(`{"type":"control_request","request_id":"approval-1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"echo hello"}}}`)
	if d.Decode(&v) != nil || v["type"] != "control_response" {
		os.Exit(23)
	}
	response, _ := v["response"].(map[string]any)
	result, _ := response["response"].(map[string]any)
	if result["behavior"] != "allow" {
		os.Exit(24)
	}
	fmt.Println(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}}`)
	fmt.Println(`{"type":"result","subtype":"success","result":"hello"}`)
	os.Exit(0)
}
func TestStreamProtocolAndApproval(t *testing.T) {
	t.Setenv("QUICKGUI_CLAUDE_TEST_HELPER", "1")
	exe, _ := os.Executable()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events := make(chan Frame, 10)
	done := make(chan error, 1)
	c, err := Start(ctx, Options{Binary: exe, Cwd: t.TempDir(), SessionID: "test", PermissionMode: "manual", Content: []map[string]any{{"type": "text", "text": "hello"}}}, func(f Frame) { events <- f }, func(e error) { done <- e })
	if err != nil {
		t.Fatal(err)
	}
	sawText, sawResult := false, false
	for {
		select {
		case f := <-events:
			if f.Type == "control_request" {
				if err = c.Reply(f.RequestID, map[string]any{"behavior": "allow", "updatedInput": map[string]string{"command": "echo hello"}}); err != nil {
					t.Fatal(err)
				}
			}
			if f.Type == "stream_event" {
				sawText = strings.Contains(string(f.Event), "hello")
			}
			if f.Type == "result" {
				sawResult = true
			}
		case err = <-done:
			if err != nil {
				t.Fatal(err)
			}
			for len(events) > 0 {
				f := <-events
				if f.Type == "stream_event" {
					sawText = true
				}
				if f.Type == "result" {
					sawResult = true
				}
			}
			if !sawText || !sawResult {
				t.Fatal("missing streaming/completion")
			}
			return
		case <-ctx.Done():
			t.Fatal("protocol stalled")
		}
	}
}

// Opt-in: validates the installed CLI and actual session resume in an empty
// workspace, with all tools disabled. Never runs in the ordinary test suite.
func TestInstalledClaudeConversation(t *testing.T) {
	if os.Getenv("QUICKGUI_CLAUDE_LIVE_TEST") != "1" {
		t.Skip("live Claude test disabled")
	}
	cwd := t.TempDir()
	empty := ""
	session := ""
	for turn := 0; turn < 2; turn++ {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		events := make(chan Frame, 100)
		done := make(chan error, 1)
		prompt := "Reply with exactly QUICKGUI_OK. Do not use tools."
		if turn == 1 {
			prompt = "What exact marker did you just reply with? Reply only with that marker."
		}
		_, err := Start(ctx, Options{Cwd: cwd, SessionID: session, Resume: turn > 0, PermissionMode: "manual", Tools: &empty, Content: []map[string]any{{"type": "text", "text": prompt}}}, func(f Frame) { events <- f }, func(e error) { done <- e })
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		valid := false
		consume := func(f Frame) {
			if f.SessionID != "" {
				session = f.SessionID
			}
			if f.Type == "result" {
				t.Logf("turn=%d result subtype=%s error=%v text=%q errors=%v", turn, f.Subtype, f.IsError, f.Result, f.Errors)
			}
			if f.Type == "result" && !f.IsError && strings.Contains(f.Result, "QUICKGUI_OK") {
				valid = true
			}
		}
	loop:
		for {
			select {
			case f := <-events:
				consume(f)
			case err = <-done:
				for len(events) > 0 {
					consume(<-events)
				}
				break loop
			case <-ctx.Done():
				cancel()
				t.Fatal("live Claude request timed out")
			}
		}
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if !valid || session == "" {
			t.Fatal("Claude did not complete the expected reply")
		}
	}
}
