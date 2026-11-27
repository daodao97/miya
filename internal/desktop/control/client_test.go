package control

import (
	"context"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"miya/internal/desktop"
)

func testDesktopClient(t *testing.T, d *Server, token string) *Client {
	t.Helper()
	server := httptest.NewServer(d)
	t.Cleanup(server.Close)
	addr := server.Listener.Addr().String()
	return NewClient(func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}, token)
}

func TestDesktopClientConsentCaptureInputAndStop(t *testing.T) {
	d, token, calls := mockDesktop(t)
	c := testDesktopClient(t, d, token)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := c.Session(ctx)
	if err != nil || session.WindowOnly || session.Window != nil {
		t.Fatal(session, err)
	}
	if _, _, err := c.Frame(ctx); Status(err) != 403 {
		t.Fatal("captured before consent", err)
	}
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	data, _, err := c.Frame(ctx)
	if err != nil || string(data) != "jpeg" {
		t.Fatal(string(data), err)
	}
	if err := c.input(ctx, desktop.Input{Kind: "click", X: .3, Y: .5}); err != nil || calls.Load() != 1 {
		t.Fatal("input did not reach the Mac", err, calls.Load())
	}
	if err := c.input(ctx, desktop.Input{Kind: "click", X: 1.5}); err == nil || calls.Load() != 1 {
		t.Fatal("invalid input was sent")
	}
	c.Stop()
	if _, _, err := c.Frame(ctx); Status(err) != 410 {
		t.Fatal("frame after stop", err)
	}
}

func TestDesktopClientRejectsWrongToken(t *testing.T) {
	d, _, _ := mockDesktop(t)
	c := testDesktopClient(t, d, "wrong")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Session(ctx); Status(err) != 410 {
		t.Fatal("wrong token accepted", err)
	}
}

func TestDesktopClientFrameQuality(t *testing.T) {
	ctx := context.Background()
	for _, adaptive := range []bool{false, true} {
		d, token, _ := mockDesktop(t)
		want := desktop.FrameOptions{MaxEdge: 3024, Quality: 90}
		if adaptive {
			d.CaptureFrame = func(_ context.Context, display *desktop.Display, window *desktop.Window, options desktop.FrameOptions) ([]byte, error) {
				if display != nil || window != nil || options != want {
					t.Errorf("unexpected capture target or options: %v %v %+v", display, window, options)
				}
				return []byte("adaptive"), nil
			}
		}
		c := testDesktopClient(t, d, token)
		if _, _, err := c.FrameWithOptions(ctx, want); Status(err) != 403 {
			t.Fatalf("adaptive capture before consent: %v", err)
		}
		if err := c.Start(ctx); err != nil {
			t.Fatal(err)
		}
		if _, _, err := c.FrameWithOptions(ctx, desktop.FrameOptions{MaxEdge: MaxFrameEdge + 1, Quality: 90}); Status(err) != 400 {
			t.Fatalf("invalid frame request: %v", err)
		}
		data, _, err := c.FrameWithOptions(ctx, want)
		expected := "jpeg"
		if adaptive {
			expected = "adaptive"
		}
		if err != nil || string(data) != expected {
			t.Fatalf("frame: %q, %v, want %q", data, err, expected)
		}
		c.Stop()
	}
}

func TestDesktopInteractiveFrameRate(t *testing.T) {
	ctx := context.Background()
	for _, pick := range []bool{false, true} {
		d, token, _ := mockDesktop(t)
		d.CaptureFrame = func(_ context.Context, _ *desktop.Display, _ *desktop.Window, options desktop.FrameOptions) ([]byte, error) {
			return []byte("adaptive"), nil
		}
		if pick {
			d.AllowPick(token)
		}
		c := testDesktopClient(t, d, token)
		session, err := c.Session(ctx)
		if err != nil || session.InteractiveFrames != pick {
			t.Fatalf("interactive capability for pick=%v: %+v, %v", pick, session, err)
		}
		if err = c.Start(ctx); err != nil {
			t.Fatal(err)
		}
		options := desktop.FrameOptions{MaxEdge: InteractiveFrameMaxEdge, Quality: 65}
		setLastFrame := func() {
			d.mu.Lock()
			d.lease.lastFrame = time.Now().Add(-150 * time.Millisecond)
			d.mu.Unlock()
		}
		setLastFrame()
		if _, _, err = c.FrameWithOptions(ctx, options); Status(err) != 429 {
			t.Fatalf("ordinary frame bypassed its rate limit: %v", err)
		}
		setLastFrame()
		_, _, err = c.InteractiveFrame(ctx, options)
		if pick && err != nil || !pick && Status(err) != 429 {
			t.Fatalf("interactive frame rate for pick=%v: %v", pick, err)
		}
		setLastFrame()
		options.MaxEdge++
		if _, _, err = c.InteractiveFrame(ctx, options); Status(err) != 429 {
			t.Fatalf("large frame bypassed its rate limit: %v", err)
		}
	}
}
