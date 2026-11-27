package control

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"miya/internal/desktop"
)

func TestDesktopStreamPushesFramesAndAcceptsOrderedInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	d, token, _ := mockDesktop(t)
	d.AllowPick(token)
	d.Windows = func() ([]desktop.Window, error) { return nil, nil }
	frames := new(atomic.Int32)
	d.CaptureFrame = func(_ context.Context, _ *desktop.Display, _ *desktop.Window, options desktop.FrameOptions) ([]byte, error) {
		if options.MaxEdge > InteractiveFrameMaxEdge {
			t.Errorf("unexpected stream size: %d", options.MaxEdge)
		}
		return fmt.Appendf(nil, "frame %d", frames.Add(1)), nil
	}
	input := make(chan desktop.Input, 3)
	d.Input = func(in desktop.Input) error {
		input <- in
		return nil
	}
	client := testDesktopClient(t, d, token)
	session, err := client.Session(ctx)
	if err != nil || !session.FrameStream {
		t.Fatalf("stream capability: %+v, %v", session, err)
	}
	if stream, err := client.OpenFrameStream(ctx); Status(err) != 403 {
		if stream != nil {
			stream.Close()
		}
		t.Fatalf("accepted stream before Start: %v", err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	stream, err := client.OpenFrameStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for i := 1; i <= streamFrameCredits; i++ {
		kind, data, err := stream.conn.Read(ctx)
		if kind != websocket.MessageBinary || string(data) != fmt.Sprintf("frame %d", i) || err != nil {
			t.Fatalf("push frame: %q, %v", data, err)
		}
	}
	// Send several inputs before reading any replies or acknowledging frames.
	for i := 1; i <= 3; i++ {
		in := desktop.Input{Kind: "scroll", X: .5, Y: .5, DeltaY: i}
		if err := stream.SendInput(ctx, in, desktop.Display{}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 3; i++ {
		select {
		case got := <-input:
			if got.DeltaY != i {
				t.Fatalf("scroll order: got %+v, want delta %d", got, i)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	Wait(ctx, 2*streamFrameInterval)
	if got := frames.Load(); got != streamFrameCredits {
		t.Fatalf("unacknowledged frames in flight: %d", got)
	}
	if err := stream.conn.Write(ctx, websocket.MessageText, []byte("ack")); err != nil {
		t.Fatal(err)
	}
	if data, notice, err := stream.Receive(ctx); string(data) != fmt.Sprintf("frame %d", streamFrameCredits+1) || notice != nil || err != nil {
		t.Fatalf("frame after ACK: %q, %+v, %v", data, notice, err)
	}
	if err := client.SelectWindow(ctx, desktop.Window{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := stream.Receive(ctx); err == nil {
		t.Fatal("stream remained active after target change")
	}
}

func TestDesktopStreamSkipsUnchangedFramesAndPushesChanges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	d, token, _ := mockDesktop(t)
	d.AllowPick(token)
	var scene atomic.Int32
	var captures atomic.Int32
	d.CaptureFrame = func(context.Context, *desktop.Display, *desktop.Window, desktop.FrameOptions) ([]byte, error) {
		value := scene.Load()
		captures.Add(1)
		return []byte{byte(value)}, nil
	}
	client := testDesktopClient(t, d, token)
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	stream, err := client.OpenFrameStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if data, notice, err := stream.Receive(ctx); len(data) != 1 || data[0] != 0 || notice != nil || err != nil {
		t.Fatalf("initial frame %v, %+v, %v", data, notice, err)
	}
	for captures.Load() < 3 && Wait(ctx, 5*time.Millisecond) {
	}
	scene.Store(1)
	if data, notice, err := stream.Receive(ctx); len(data) != 1 || data[0] != 1 || notice != nil || err != nil {
		t.Fatalf("next frame %v, %+v, %v", data, notice, err)
	}
}
