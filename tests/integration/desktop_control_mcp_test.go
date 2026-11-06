//go:build !windows

package integration

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	codex "miya/internal/agent/codex/native"
	"miya/internal/async"
	"miya/internal/chat/model"
	"miya/internal/desktop"
	mcpserver "miya/internal/mcp"
	mcpproto "miya/internal/mcp/protocol"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
)

type desktopEOFConnection struct{ *testPhoneConnection }

func (c desktopEOFConnection) Receive(v any) error {
	select {
	case r, ok := <-c.requests:
		if !ok {
			return io.EOF
		}
		*v.(*remote.Request) = r
		return nil
	case <-c.ctx.Done():
		return io.EOF
	}
}

func TestDesktopMCPWaitsForExplicitCompletion(t *testing.T) {
	c := serverController(t.TempDir(), "")
	caller := model.Thread{ID: 7, ServerID: "caller", Busy: true, Messages: []model.Message{{Kind: "mcpToolCall", Status: "inProgress", Item: &model.Activity{Server: "miya", Tool: "request_desktop_control"}}}}
	c.SetThreads([]model.Thread{caller})
	v, err := c.MCP().DesktopControl(mcpproto.Request{Tool: "request_desktop_control", Task: "在 Chrome 完成登录"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Approvals()) != 1 || c.Approvals()[0].ThreadID != "caller" || c.Approvals()[0].Method != "miya/desktopControl" {
		t.Fatal("takeover card not bound to caller")
	}
	if c.Desktop != nil {
		t.Fatal("agent request started desktop capture")
	}
	done := make(chan string, 1)
	go func() {
		result, err := v.(async.Reply)()
		if err != nil {
			done <- err.Error()
			return
		}
		done <- result.(string)
	}()
	select {
	case <-done:
		t.Fatal("request completed without user confirmation")
	default:
	}
	a := c.Approvals()[0]
	if err := c.PhoneApprovalResponse(remote.Request{Approval: a.ID, ApprovalVersion: phoneproto.ApprovalFingerprint(a), Allow: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case text := <-done:
		if !strings.Contains(text, "The user confirmed") {
			t.Fatal(text)
		}
	case <-time.After(time.Second):
		t.Fatal("confirmation did not resume agent")
	}
	if len(c.Approvals()) != 0 {
		t.Fatal("completed takeover card retained")
	}
}

func TestDesktopMCPReasonAndToolRouting(t *testing.T) {
	s := mcpserver.NewServer("/ws", func(op string, args, out any) error {
		r := args.(mcpproto.Request)
		if op != "mcpDesktopControl" || r.Tool != "request_desktop_control" || r.Task != "登录" {
			t.Fatalf("bad call: %s %+v", op, r)
		}
		assign(out, "done")
		return nil
	}, nil)
	if text, err := s.CallTool("request_desktop_control", json.RawMessage(`{"reason":"登录"}`)); err != nil || text != "done" {
		t.Fatal(text, err)
	}
}

func TestDesktopPhoneOpenRequiresStableConversation(t *testing.T) {
	if !desktop.Available() {
		t.Skip("host capability unavailable")
	}
	c := serverController(t.TempDir(), "")
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "s1", HistoryLoaded: true}})
	ctx, cancel := context.WithCancel(context.Background())
	conn := &testPhoneConnection{ctx: ctx, requests: make(chan remote.Request, 8), frames: make(chan remote.Frame, 32)}
	ex := async.NewExecutor()
	quit, done, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	c.Run = ex.Push
	go func() { defer close(exited); ex.Loop(quit) }()
	go func() { defer close(done); c.ServePhoneConnection(ctx, desktopEOFConnection{conn}) }()
	t.Cleanup(func() { cancel(); <-done; close(quit); <-exited })
	ask := func(r remote.Request) remote.Frame {
		conn.requests <- r
		for {
			select {
			case f := <-conn.frames:
				if f.Type == "desktop-open" {
					return f
				}
			case <-conn.ctx.Done():
				t.Fatal("no desktop reply")
			}
		}
	}
	if f := ask(remote.Request{Op: "desktop-open", Thread: 1, ServerID: "missing", RequestID: "stale"}); f.Error == "" {
		t.Fatal("stale conversation accepted")
	}
	f := ask(remote.Request{Op: "desktop-open", Thread: 1, ServerID: "s1", RequestID: "open"})
	var reply phoneproto.DesktopReply
	if json.Unmarshal(f.Data, &reply) != nil || f.Error != "" || reply.RequestID != "open" || reply.Token == "" {
		t.Fatal("no desktop lease", f.Error)
	}
	if c.Desktop.Capturing() {
		t.Fatal("opening popup granted capture")
	}
	leaseDone := c.Desktop.LeaseDone()
	close(conn.requests)
	select {
	case <-leaseDone:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not cancel lease")
	}
	if c.Desktop.Holds(reply.Token) {
		t.Fatal("lease survived control disconnection")
	}
}

func TestDesktopMobileTakeoverEntry(t *testing.T) {
	for _, width := range []int{320, 390} {
		m := testMobileApp()
		m.State.DesktopControl = true
		th := m.State.Projects[0].Threads[0]
		m.OpenThread(th)
		m.ShowThread(th)
		a := codex.Approval{ID: json.RawMessage(`"miya:1:1"`), Method: "miya/desktopControl", ThreadID: th.ServerID, Reason: "登录授权"}
		m.State.Approvals = []codex.Approval{a}
		tt := ui.NewTester(m.ViewRoot, width, 844)
		tt.Frame()
		for _, label := range []string{"Open remote desktop", "Done, continue", "Cancel takeover"} {
			r, ok := tt.Find(label)
			if !ok || r.H < 44 || r.X < 0 || r.X+r.W > float32(width) {
				t.Fatalf("takeover action unavailable: %s %v", label, r)
			}
		}
		saveMobileNativeSnapshot(t, tt, "desktop-takeover")
	}
}
