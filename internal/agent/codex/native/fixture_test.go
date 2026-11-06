package codex

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFixtureInteractions(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("fixture requires Python 3")
	}
	binary, _ := filepath.Abs("../../../../testdata/codex-server.py")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Start(ctx, binary, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var thread ThreadResponse
	if err = c.Call(ctx, "thread/start", map[string]any{}, &thread); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"all", "approval", "file", "permissions", "question", "mcp", "long", "error"} {
		t.Run(scenario, func(t *testing.T) {
			var result struct {
				Turn Turn `json:"turn"`
			}
			if err := c.Call(ctx, "turn/start", map[string]any{"threadId": thread.Thread.ID, "input": []map[string]string{{"type": "text", "text": scenario}}}, &result); err != nil {
				t.Fatal(err)
			}
			if scenario == "long" {
				if err := c.Call(ctx, "turn/interrupt", map[string]string{"threadId": thread.Thread.ID, "turnId": result.Turn.ID}, nil); err != nil {
					t.Fatal(err)
				}
			}
			requestSeen := false
			final := ""
			status := ""
		loop:
			for {
				select {
				case f, ok := <-c.Events():
					if !ok {
						t.Fatal(c.Err())
					}
					if len(f.ID) > 0 {
						requestSeen = true
						var response any
						switch f.Method {
						case "item/commandExecution/requestApproval":
							response = map[string]string{"decision": "accept"}
						case "item/fileChange/requestApproval":
							response = map[string]string{"decision": "decline"}
						case "item/permissions/requestApproval":
							response = map[string]any{"permissions": map[string]any{}, "scope": "turn"}
						case "item/tool/requestUserInput":
							response = map[string]any{"answers": map[string]any{"color": map[string]any{"answers": []string{"Blue"}}, "name": map[string]any{"answers": []string{"Tester"}}}}
						case "mcpServer/elicitation/request":
							response = map[string]any{"action": "accept", "content": map[string]string{"label": "verified"}}
						default:
							t.Fatal(f.Method)
						}
						if err := c.Reply(f.ID, response); err != nil {
							t.Fatal(err)
						}
						continue
					}
					var e Event
					if err := json.Unmarshal(f.Params, &e); err != nil {
						t.Fatal(err)
					}
					if e.TurnID != "" && e.TurnID != result.Turn.ID {
						continue
					}
					if f.Method == "item/completed" && e.Item.Type == "agentMessage" {
						final = e.Item.Text
					}
					if f.Method == "turn/completed" && e.Turn.ID == result.Turn.ID {
						status = e.Turn.Status
						break loop
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			switch scenario {
			case "all":
				if !strings.Contains(final, "authoritative") {
					t.Fatal(final)
				}
			case "long":
				if status != "interrupted" {
					t.Fatal(status)
				}
			case "error":
				if status != "failed" {
					t.Fatal(status)
				}
			default:
				if !requestSeen || !strings.Contains(final, "Server received") {
					t.Fatal("request was not round-tripped")
				}
			}
		})
	}
}
