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
	mode := os.Getenv("MIYA_CLAUDE_AGENT_TEST_HELPER")
	if mode == "" {
		return
	}
	d := json.NewDecoder(os.Stdin)
	var in map[string]any
	if d.Decode(&in) != nil {
		os.Exit(31)
	}
	fmt.Println(`{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}`)
	if d.Decode(&in) != nil {
		os.Exit(32)
	}
	fmt.Println(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call1","name":"Agent","input":{"description":"Review"}}]}}`)
	fmt.Println(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call1","content":"Launched"}]},"tool_use_result":{"isAsync":true,"agentId":"a1"}}`)
	fmt.Println(`{"type":"result","result":"Waiting"}`)
	if mode == "exit" {
		os.Exit(0)
	}
	// Receiving this probe after result proves that Miya kept stdin open.
	if d.Decode(&in) != nil {
		os.Exit(33)
	}
	if mode == "stop" {
		request, _ := in["request"].(map[string]any)
		if request["subtype"] != "interrupt" {
			os.Exit(34)
		}
		os.Exit(0)
	}
	fmt.Println(`{"type":"result","parent_tool_use_id":"call1","result":"Child result"}`)
	if d.Decode(&in) != nil {
		os.Exit(35)
	}
	fmt.Println(`{"type":"system","subtype":"task_notification","task_id":"a1","status":"completed","summary":"Review complete"}`)
	fmt.Println(`{"type":"assistant","message":{"id":"summary","content":[{"type":"text","text":"Final summary"}]}}`)
	fmt.Println(`{"type":"result","result":"Final summary"}`)
	if d.Decode(&in) == nil {
		os.Exit(36)
	}
	os.Exit(0)
}

func TestAgentSessionSurvivesParentAndChildResults(t *testing.T) {
	exe, _ := os.Executable()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	frames, done := make(chan Frame, 20), make(chan error, 1)
	c, err := Start(ctx, Options{Binary: exe, Cwd: t.TempDir(), Env: []string{"MIYA_CLAUDE_AGENT_TEST_HELPER=complete"}}, func(f Frame) { frames <- f }, func(err error) { done <- err })
	if err != nil {
		t.Fatal(err)
	}
	parentResults, children := 0, 0
	for {
		select {
		case f := <-frames:
			if f.Type != "result" {
				continue
			}
			if f.ParentToolUseID != "" {
				children++
			} else {
				parentResults++
				if parentResults == 1 && (!f.KeepingAlive || !f.WaitingForAgents) {
					t.Fatal("parent result closed background agent")
				}
				if parentResults == 2 {
					if f.KeepingAlive || f.WaitingForAgents {
						t.Fatal("final result retained completed agent")
					}
					continue
				}
			}
			if err := c.Write(map[string]string{"type": "probe"}); err != nil {
				t.Fatalf("stdin closed before task completion: %v", err)
			}
		case err := <-done:
			for len(frames) > 0 {
				f := <-frames
				if f.Type == "result" && f.ParentToolUseID == "" {
					parentResults++
				}
			}
			if err != nil || parentResults != 2 || children != 1 {
				t.Fatalf("parents=%d children=%d err=%v", parentResults, children, err)
			}
			return
		case <-ctx.Done():
			t.Fatal("background session stalled")
		}
	}
}

func TestAgentSessionEarlyExitAndStop(t *testing.T) {
	for _, mode := range []string{"exit", "stop"} {
		t.Run(mode, func(t *testing.T) {
			exe, _ := os.Executable()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			frames, done := make(chan Frame, 20), make(chan error, 1)
			c, err := Start(ctx, Options{Binary: exe, Cwd: t.TempDir(), Env: []string{"MIYA_CLAUDE_AGENT_TEST_HELPER=" + mode}}, func(f Frame) { frames <- f }, func(err error) { done <- err })
			if err != nil {
				t.Fatal(err)
			}
			for {
				select {
				case f := <-frames:
					if f.Type == "result" && mode == "stop" {
						c.Stop()
					}
				case err := <-done:
					if err == nil || !strings.Contains(err.Error(), "子 agent 完成前") {
						t.Fatalf("incomplete task exit was not reported: %v", err)
					}
					return
				case <-ctx.Done():
					t.Fatal("stop or exit stalled")
				}
			}
		})
	}
}
