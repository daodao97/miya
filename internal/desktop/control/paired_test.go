package control_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"miya/internal/desktop"
	"miya/internal/desktop/control"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
)

func TestPairedDesktopLeaseOverWorkspaceTunnel(t *testing.T) {
	if os.Getenv("MIYA_REMOTE_E2E") != "1" {
		t.Skip("set MIYA_REMOTE_E2E=1 to test through the public relays")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	input := make(chan desktop.Input, 1)
	host := &control.Server{Native: control.Native{
		Authorize: func() error { return nil },
		Windows:   func() ([]desktop.Window, error) { return nil, nil },
		Capture:   func() ([]byte, error) { return []byte("test frame"), nil },
		CaptureFrame: func(_ context.Context, _ *desktop.Display, _ *desktop.Window, options desktop.FrameOptions) ([]byte, error) {
			if options.MaxEdge == control.InteractiveFrameMaxEdge {
				return []byte("interactive frame"), nil
			}
			return []byte("test frame"), nil
		},
		Input: func(in desktop.Input) error { input <- in; return nil },
	}}
	leases := make(chan string, 1)
	revoked := make(chan string, 2)
	bridge, err := remote.Start(ctx, remote.Options{
		StateDir: t.TempDir(),
		Files:    host,
		Service: func(_ context.Context, c *remote.Conn) {
			for {
				var request string
				if c.Receive(&request) != nil || c.Send(request) != nil {
					return
				}
			}
		},
		Serve: func(_ context.Context, c *remote.Conn) {
			if c.Send(remote.Frame{Type: "hello", Version: remote.ProtocolVersion}) != nil || c.CompressSends() != nil {
				return
			}
			var request remote.Request
			if c.Receive(&request) != nil {
				return
			}
			if request.Op != "desktop-open" || !request.WindowPick || request.RequestID == "" {
				t.Errorf("unexpected lease request: %+v", request)
				return
			}
			token, err := host.OpenWindow(c.Context(), &desktop.Window{})
			if err != nil {
				t.Error(err)
				return
			}
			defer func() {
				host.Revoke(token)
				revoked <- token
			}()
			host.AllowPick(token)
			data, _ := json.Marshal(phoneproto.DesktopReply{RequestID: request.RequestID, Token: token})
			if c.Send(remote.Frame{Type: "desktop-open", Data: data}) == nil {
				leases <- token
				_ = c.Receive(&request)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	workspace, err := remote.ConnectService(ctx, bridge.Link())
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()
	_ = workspace.SetReadDeadline(time.Now().Add(20 * time.Second))
	viewCtx, closeView := context.WithCancel(ctx)
	defer closeView()
	client, done, err := control.OpenPaired(viewCtx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if token := <-leases; token != client.Token() {
		t.Fatal("viewer got a different lease")
	}
	if session, err := client.Session(ctx); err != nil || !session.FrameStream {
		t.Fatalf("interactive frame capability: %+v, %v", session, err)
	}
	if err := client.SelectWindow(ctx, desktop.Window{}); err != nil {
		t.Fatal(err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if data, _, err := client.Frame(ctx); string(data) != "test frame" || err != nil {
		t.Fatalf("frame %q: %v", data, err)
	}
	want := desktop.Input{Kind: "click", X: 0.25, Y: 0.75}
	if err := client.InputForDisplay(ctx, want, desktop.Display{}); err != nil {
		t.Fatal(err)
	}
	if got := <-input; got != want {
		t.Fatalf("input: %+v", got)
	}
	scroll := desktop.Input{Kind: "scroll", X: 0.25, Y: 0.75, DeltaX: 3, DeltaY: -3}
	if !control.Wait(ctx, 30*time.Millisecond) {
		t.Fatal(ctx.Err())
	}
	if err := client.InputForDisplay(ctx, scroll, desktop.Display{}); err != nil {
		t.Fatal(err)
	}
	if got := <-input; got != scroll {
		t.Fatalf("trackpad scroll: %+v", got)
	}
	if !control.Wait(ctx, control.InteractiveFrameInterval) {
		t.Fatal(ctx.Err())
	}
	options := desktop.FrameOptions{MaxEdge: control.InteractiveFrameMaxEdge, Quality: 65}
	if data, _, err := client.InteractiveFrame(ctx, options); err != nil || string(data) != "interactive frame" {
		t.Fatalf("interactive frame %q: %v", data, err)
	}
	stream, err := client.OpenFrameStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if data, notice, err := stream.Receive(ctx); string(data) != "interactive frame" || notice != nil || err != nil {
		t.Fatalf("streamed frame %q: %+v, %v", data, notice, err)
	}
	scroll.DeltaY = -8
	if err := stream.SendInput(ctx, scroll, desktop.Display{}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-input:
		if got != scroll {
			t.Fatalf("streamed input: %+v", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	closeView()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("closing viewer did not close its lease stream")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := client.Session(ctx)
		if control.Status(err) == 410 {
			break
		}
		if time.Now().After(deadline) || !control.Wait(ctx, 25*time.Millisecond) {
			t.Fatalf("lease remained available after close: %v", err)
		}
	}
	if err := workspace.Send("still connected"); err != nil {
		t.Fatal(err)
	}
	_ = workspace.SetReadDeadline(time.Now().Add(5 * time.Second))
	var reply string
	if err := workspace.Receive(&reply); reply != "still connected" || err != nil {
		t.Fatalf("workspace did not survive viewer close: %q, %v", reply, err)
	}
	if token := <-revoked; token != client.Token() {
		t.Fatal("the first lease was not revoked")
	}
	second, secondDone, err := control.OpenPaired(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	_ = workspace.Close()
	select {
	case <-secondDone:
	case <-ctx.Done():
		t.Fatal("closing the workspace did not close the viewer")
	}
	select {
	case token := <-revoked:
		if token != second.Token() {
			t.Fatal("workspace close revoked the wrong lease")
		}
	case <-ctx.Done():
		t.Fatal("the second lease outlived the workspace connection")
	}
}
