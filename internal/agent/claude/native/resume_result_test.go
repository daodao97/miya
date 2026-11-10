package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func init() {
	mode := os.Getenv("MIYA_CLAUDE_RESUME_RESULT_HELPER")
	if mode == "" {
		return
	}
	d := json.NewDecoder(os.Stdin)
	var input map[string]any
	if d.Decode(&input) != nil {
		os.Exit(61)
	}
	if mode == "before-initialize" {
		fmt.Println(`{"type":"result","subtype":"success","result":""}`)
	}
	fmt.Println(`{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}`)
	if d.Decode(&input) != nil {
		os.Exit(62)
	}
	if mode == "continue" {
		fmt.Println(`{"type":"user","message":{"content":"<task-notification><task-id>old-task</task-id><status>stopped</status><summary>Previous session ended</summary></task-notification>"}}`)
	}
	if mode == "api-error" {
		fmt.Println(`{"type":"result","is_error":true,"errors":["upstream unavailable"]}`)
	} else if mode != "before-initialize" {
		fmt.Println(`{"type":"result","subtype":"success","result":""}`)
	}
	if mode == "empty-then-error" {
		fmt.Println(`{"type":"result","is_error":true,"errors":["upstream unavailable"]}`)
	}
	if mode == "continue" || mode == "before-initialize" {
		// Restoration finishes before the submitted prompt begins. A tool
		// approval proves stdin survived; a slow reply outlives the old kill timer.
		time.Sleep(50 * time.Millisecond)
		fmt.Println(`{"type":"command_lifecycle","state":"started"}`)
		fmt.Println(`{"type":"control_request","request_id":"probe","request":{"subtype":"can_use_tool","tool_name":"Read"}}`)
		if d.Decode(&input) != nil || input["type"] != "control_response" {
			os.Exit(63)
		}
		if mode == "continue" {
			time.Sleep(5200 * time.Millisecond)
		}
		fmt.Println(`{"type":"assistant","message":{"id":"reply","content":[{"type":"text","text":"Resumed successfully"}]}}`)
		fmt.Println(`{"type":"result","result":"Resumed successfully"}`)
	} else if mode == "empty-eof" {
		os.Exit(0)
	}
	if d.Decode(&input) == nil {
		os.Exit(64)
	}
	os.Exit(0)
}

func TestResumeResultBelongsToSubmittedTurn(t *testing.T) {
	for _, mode := range []string{"continue", "before-initialize", "empty-eof", "empty-idle", "api-error", "empty-then-error"} {
		t.Run(mode, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			frames, done := make(chan Frame, 20), make(chan error, 1)
			client, err := Start(ctx, Options{Binary: exe, Cwd: t.TempDir(), Resume: true,
				Env: []string{"MIYA_CLAUDE_RESUME_RESULT_HELPER=" + mode}},
				func(f Frame) { frames <- f }, func(err error) { done <- err })
			if err != nil {
				t.Fatal(err)
			}
			var results []Frame
			consume := func(f Frame) {
				if f.Type == "control_request" {
					if err := client.Reply(f.RequestID, map[string]any{"behavior": "deny"}); err != nil {
						t.Errorf("resumed turn lost stdin: %v", err)
					}
				}
				if f.Type == "result" {
					results = append(results, f)
				}
			}
			for {
				select {
				case f := <-frames:
					consume(f)
				case err := <-done:
					for len(frames) > 0 {
						consume(<-frames)
					}
					if err != nil || len(results) != 1 {
						t.Fatalf("result count=%d process error=%v", len(results), err)
					}
					result := results[0]
					if mode == "continue" || mode == "before-initialize" {
						if result.Result != "Resumed successfully" {
							t.Fatalf("restoration replaced the submitted turn: %+v", result)
						}
					} else if mode == "api-error" || mode == "empty-then-error" {
						if !result.IsError || len(result.Errors) != 1 || result.Errors[0] != "upstream unavailable" {
							t.Fatalf("lost API failure: %+v", result)
						}
					} else if result.IsError || result.Result != "" || result.KeepingAlive {
						t.Fatalf("lost genuine empty result: %+v", result)
					}
					return
				case <-ctx.Done():
					t.Fatal("resumed process did not finish")
				}
			}
		})
	}
}
