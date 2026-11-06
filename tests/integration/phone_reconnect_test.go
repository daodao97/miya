//go:build !windows

package integration

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
	"time"

	codex "miya/internal/agent/codex/native"
	"miya/internal/appmodel"
	"miya/internal/async"
	"miya/internal/controller"
	"miya/internal/model"
	"miya/internal/phoneapi"
	"miya/internal/remote"
)

// Drive the real phone-serving loop without a relay or native sockets.
type testPhoneConnection struct {
	ctx      context.Context
	requests chan remote.Request
	frames   chan remote.Frame
}

func (c *testPhoneConnection) CompressSends() error { return nil }

func (c *testPhoneConnection) Send(v any) error {
	select {
	case c.frames <- v.(remote.Frame):
		return nil
	case <-c.ctx.Done():
		return io.EOF
	}
}

func (c *testPhoneConnection) Receive(v any) error {
	select {
	case r := <-c.requests:
		*v.(*remote.Request) = r
		return nil
	case <-c.ctx.Done():
		return io.EOF
	}
}

func serveTestPhone(t *testing.T, c *controller.Controller) (*testPhoneConnection, *async.Executor) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn := &testPhoneConnection{ctx: ctx, requests: make(chan remote.Request, 8), frames: make(chan remote.Frame, 32)}
	ex := async.NewExecutor()
	quit, done, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	c.Run = ex.Push
	go func() { defer close(exited); ex.Loop(quit) }()
	go func() { defer close(done); c.ServePhoneConnection(ctx, conn) }()
	t.Cleanup(func() {
		cancel()
		<-done
		close(quit)
		<-exited
	})
	return conn, ex
}

func (c *testPhoneConnection) nextThread(t *testing.T) phoneapi.ThreadDelta {
	t.Helper()
	for {
		select {
		case f := <-c.frames:
			if f.Type != "thread" {
				continue
			}
			var d phoneapi.ThreadDelta
			if err := json.Unmarshal(f.Data, &d); err != nil {
				t.Fatal(err)
			}
			if f.Thread != d.Thread.ID {
				t.Fatalf("frame and thread IDs differ: %+v", f)
			}
			return d
		case <-c.ctx.Done():
			t.Fatal("phone-serving loop did not return a thread")
			return phoneapi.ThreadDelta{}
		}
	}
}

func TestPhoneSubscriptionRecoversWhenStartupListArrives(t *testing.T) {
	c := serverController(t.TempDir(), "")
	c.Connecting = true
	conn, ex := serveTestPhone(t, c)
	conn.requests <- remote.Request{Op: "subscribe", Thread: 345, ServerID: "original", Agent: appmodel.AgentCodex}
	if d := conn.nextThread(t); !d.Thread.Loading || d.Thread.ServerID != "original" {
		t.Fatalf("startup incorrectly reported a deleted conversation: %+v", d)
	}
	ex.Do(func() {
		c.Connecting = false
		c.SetReady(true)
		c.SetThreads([]model.Thread{
			{ID: 345, ServerID: "different", HistoryLoaded: true},
			{ID: 8, ServerID: "original", HistoryLoaded: true, Messages: []model.Message{{ID: 1, Content: "Original reply"}}},
		})
		// Startup publishes the list without incrementing stream revision.
	})
	conn.requests <- remote.Request{Op: "wake"}
	d := conn.nextThread(t)
	if d.Thread.ID != 8 || d.Thread.ServerID != "original" || len(d.Changed) != 1 || d.Changed[0].Content != "Original reply" {
		t.Fatalf("subscription did not recover by stable identity: %+v", d)
	}
	// The stable identity also works without any numeric ID from the phone.
	conn.requests <- remote.Request{Op: "subscribe", ServerID: "original"}
	if d := conn.nextThread(t); d.Thread.ID != 8 || d.Thread.ServerID != "original" || len(d.Changed) != 1 {
		t.Fatalf("subscription required a desktop-local number: %+v", d)
	}
}

func TestPhoneRetrySameSubscriptionReadsHistoryAgain(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QUICKGUI_FIXTURE_RESUME", "1")
	bin, err := filepath.Abs("testdata/codex-server.py")
	if err != nil {
		t.Fatal(err)
	}
	c := serverController(t.TempDir(), "")
	c.SetReady(true)
	c.SetThreads([]model.Thread{{ID: 1, ServerID: "resume-thread"}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := codex.Start(ctx, bin, c.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	conn, ex := serveTestPhone(t, c)
	req := remote.Request{Op: "subscribe", Thread: 1, ServerID: "resume-thread"}
	conn.requests <- req
	if d := conn.nextThread(t); d.Thread.Status != "Desktop is connecting to Codex…" {
		t.Fatalf("expected a temporary history-read error, got %+v", d)
	}
	// Neither sidebar metadata nor its revision changes as the client returns.
	ex.Do(func() { c.Client = client })
	conn.requests <- req
	d := conn.nextThread(t)
	if d.Thread.Status != "" || !d.Thread.HistoryLoaded || len(d.Changed) != 2 {
		t.Fatalf("retry reused the cached history-read failure: %+v", d)
	}
}
