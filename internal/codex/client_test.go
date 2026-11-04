package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func testConnection(t *testing.T) (*Client, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	c := newClient(a)
	go c.read(a)
	t.Cleanup(func() { c.Close(); b.Close() })
	return c, b
}

func TestWithToolchainPathAddsHomebrewLocations(t *testing.T) {
	env := withToolchainPath([]string{"HOME=/tmp", "PATH=/usr/bin:/bin"})
	var path string
	for _, item := range env {
		if strings.HasPrefix(item, "PATH=") {
			path = strings.TrimPrefix(item, "PATH=")
		}
	}
	if !strings.HasPrefix(path, "/usr/local/sbin:/usr/local/bin:/opt/homebrew/sbin:/opt/homebrew/bin:") {
		t.Fatalf("toolchain paths were not prepended: %s", path)
	}
	updated := withToolchainPath(env)
	var updatedPath string
	for _, item := range updated {
		if strings.HasPrefix(item, "PATH=") {
			updatedPath = strings.TrimPrefix(item, "PATH=")
		}
	}
	if strings.Count(updatedPath, "/opt/homebrew/bin") > 1 {
		t.Fatal("toolchain path was duplicated")
	}
}

func TestMultiplexAndServerRequest(t *testing.T) {
	c, server := testConnection(t)
	go func() {
		s := bufio.NewScanner(server)
		var requests []Frame
		for len(requests) < 2 && s.Scan() {
			var f Frame
			json.Unmarshal(s.Bytes(), &f)
			requests = append(requests, f)
		}
		if len(requests) < 2 {
			return
		}
		for i := 1; i >= 0; i-- {
			fmt.Fprintf(server, "{\"id\":%s,\"result\":{\"name\":%q}}\n", requests[i].ID, requests[i].Method)
		}
		fmt.Fprintln(server, `{"id":7,"method":"item/fileChange/requestApproval","params":{"threadId":"t"}}`)
		s.Scan()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results := make(chan error, 2)
	for _, name := range []string{"first", "second"} {
		go func(name string) {
			var out struct{ Name string }
			err := c.Call(ctx, name, map[string]any{}, &out)
			if err == nil && out.Name != name {
				err = fmt.Errorf("wrong correlation: %s", out.Name)
			}
			results <- err
		}(name)
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	select {
	case f := <-c.Events():
		if string(f.ID) != "7" {
			t.Fatalf("server ID changed: %s", f.ID)
		}
		if err := c.Reply(f.ID, map[string]string{"decision": "decline"}); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
func TestErrorCancellationAndEOF(t *testing.T) {
	c, server := testConnection(t)
	go func() {
		s := bufio.NewScanner(server)
		for s.Scan() {
			var f Frame
			json.Unmarshal(s.Bytes(), &f)
			switch f.Method {
			case "error":
				fmt.Fprintf(server, "{\"id\":%s,\"error\":{\"code\":-32000,\"message\":\"not logged in\"}}\n", f.ID)
			case "close":
				server.Close()
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var rpc *RPCError
	if err := c.Call(ctx, "error", nil, nil); !errors.As(err, &rpc) || rpc.Code != -32000 {
		t.Fatalf("expected RPC error, got %v", err)
	}
	short, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	if err := c.Call(short, "hang", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	c.mu.Lock()
	pending := len(c.pending)
	c.mu.Unlock()
	if pending != 0 {
		t.Fatal("request leaked after timeout")
	}
	if err := c.Call(ctx, "close", nil, nil); err == nil {
		t.Fatal("EOF did not reject pending request")
	}
}
func TestMalformedFrameFailsConnection(t *testing.T) {
	c, s := testConnection(t)
	go fmt.Fprintln(s, "not json")
	select {
	case <-c.Done():
		if !strings.Contains(c.Err().Error(), "invalid Codex") {
			t.Fatal(c.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("connection remained open")
	}
}
func TestRealServer(t *testing.T) {
	if os.Getenv("CODEX_INTEGRATION") != "1" {
		t.Skip("set CODEX_INTEGRATION=1 to test installed Codex and its configured account")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	c, err := Start(ctx, "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var models struct {
		Data []Model `json:"data"`
	}
	if err = c.Call(ctx, "model/list", map[string]any{}, &models); err != nil {
		t.Fatal(err)
	}
	t.Logf("initialized; model catalog contains %d entries", len(models.Data))
	var thread ThreadResponse
	if err = c.Call(ctx, "thread/start", map[string]any{"ephemeral": true, "sandbox": "read-only", "approvalPolicy": "never"}, &thread); err != nil {
		t.Fatal(err)
	}
	if thread.Thread.ID == "" {
		t.Fatal("missing thread ID")
	}
	for _, prompt := range []string{"Remember the verification word QUICKGUI_CODEX_OK. Reply with only that word. Do not use tools or read files.", "What was the verification word in my previous message? Reply with only that word. Do not use tools or read files."} {
		var result struct {
			Turn Turn `json:"turn"`
		}
		if err = c.Call(ctx, "turn/start", map[string]any{"threadId": thread.Thread.ID, "input": []map[string]any{{"type": "text", "text": prompt}}}, &result); err != nil {
			t.Fatal(err)
		}
		answer := ""
		deltas := 0
	loop:
		for {
			select {
			case f, ok := <-c.Events():
				if !ok {
					t.Fatal(c.Err())
				}
				if len(f.ID) > 0 {
					_ = c.Reject(f.ID, "Smoke test does not support tool requests")
					continue
				}
				var e Event
				json.Unmarshal(f.Params, &e)
				if e.ThreadID != thread.Thread.ID {
					continue
				}
				if f.Method == "item/agentMessage/delta" {
					deltas++
				}
				if f.Method == "item/completed" && e.Item.Type == "agentMessage" {
					t.Logf("native assistant phase=%q", e.Item.Phase)
					answer += e.Item.Text
				}
				if f.Method == "turn/completed" && e.Turn.ID == result.Turn.ID {
					if e.Turn.Status != "completed" {
						t.Fatalf("turn status %s; error %+v", e.Turn.Status, e.Turn.Error)
					}
					break loop
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		if !strings.Contains(answer, "QUICKGUI_CODEX_OK") {
			t.Fatalf("unexpected answer: %q", answer)
		}
		t.Logf("real turn completed; %d streamed deltas; context verified", deltas)
	}
}

func TestRealPersistence(t *testing.T) {
	if os.Getenv("CODEX_INTEGRATION") != "1" {
		t.Skip("set CODEX_INTEGRATION=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cwd := t.TempDir()
	c, err := Start(ctx, "", cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var response ThreadResponse
	if err = c.Call(ctx, "thread/start", map[string]any{"cwd": cwd, "sandbox": "read-only", "approvalPolicy": "never"}, &response); err != nil {
		t.Fatal(err)
	}
	var turn struct {
		Turn Turn `json:"turn"`
	}
	if err = c.Call(ctx, "turn/start", map[string]any{"threadId": response.Thread.ID, "input": []map[string]string{{"type": "text", "text": "Reply only OK. Do not use tools or read files."}}}, &turn); err != nil {
		t.Fatal(err)
	}
	done := false
	for !done {
		select {
		case f := <-c.Events():
			var e Event
			_ = json.Unmarshal(f.Params, &e)
			if f.Method == "turn/completed" && e.Turn.ID == turn.Turn.ID {
				if e.Turn.Status != "completed" {
					t.Fatalf("turn failed: %+v", e.Turn.Error)
				}
				done = true
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	c.Close()
	c2, err := Start(ctx, "", cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	var listing struct {
		Data []struct {
			ID     string `json:"id"`
			Source any    `json:"source"`
		} `json:"data"`
	}
	if err = c2.Call(ctx, "thread/list", map[string]any{"cwd": cwd, "sourceKinds": []string{"cli", "vscode", "exec", "appServer", "unknown"}}, &listing); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range listing.Data {
		if v.ID == response.Thread.ID {
			found = true
			t.Logf("persisted thread source: %v", v.Source)
		}
	}
	if !found {
		t.Fatal("persisted thread missing from list")
	}
	var resumed ThreadResponse
	if err = c2.Call(ctx, "thread/resume", map[string]any{"threadId": response.Thread.ID}, &resumed); err != nil {
		t.Fatal(err)
	}
	if len(resumed.Thread.Turns) == 0 {
		t.Fatal("resumed thread has no turns")
	}
	t.Log("new process restored thread history")
	_ = c2.Call(ctx, "thread/archive", map[string]string{"threadId": response.Thread.ID}, nil)
}

func TestWriteDeadlineClosesStalledTransport(t *testing.T) {
	c, _ := testConnection(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.Call(ctx, "blockedWrite", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("write timeout left a stalled connection")
	}
}
func TestAlreadyCanceledRequestIsNotSent(t *testing.T) {
	c, server := testConnection(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Call(ctx, "mustNotExecute", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	server.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	b := make([]byte, 1)
	if n, _ := server.Read(b); n != 0 {
		t.Fatal("canceled request was sent")
	}
}

func TestLateResponseDoesNotCorruptNextRequest(t *testing.T) {
	c, server := testConnection(t)
	go func() {
		s := bufio.NewScanner(server)
		for s.Scan() {
			var f Frame
			_ = json.Unmarshal(s.Bytes(), &f)
			if f.Method == "slow" {
				time.Sleep(35 * time.Millisecond)
			}
			fmt.Fprintf(server, "{\"id\":%s,\"result\":{\"method\":%q}}\n", f.ID, f.Method)
		}
	}()
	short, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	if err := c.Call(short, "slow", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	var result struct{ Method string }
	if err := c.Call(ctx, "next", nil, &result); err != nil {
		t.Fatal(err)
	}
	if result.Method != "next" {
		t.Fatal("late reply consumed by next request")
	}
}
