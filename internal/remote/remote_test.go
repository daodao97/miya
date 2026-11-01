package remote

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseLinkRejects(t *testing.T) {
	for _, raw := range []string{
		"",
		"retty://connect?v=1&address=abc",
		"miya://connect?v=2&address=abc",
		"miya://connect?v=1",
		"miya://other?v=1&address=abc",
		"miya://connect?v=1&address=not-an-address",
		"not-an-address",
		strings.Repeat("a", 5000),
	} {
		if _, err := ParseLink(raw); err == nil {
			t.Errorf("ParseLink(%.40q) accepted an invalid link", raw)
		}
	}
}

// TestBridgeEndToEnd starts a real bridge through Tailcat's public relays and
// exchanges frames with it. It needs the network, so it runs on request.
func TestBridgeEndToEnd(t *testing.T) {
	if os.Getenv("MIYA_REMOTE_E2E") != "1" {
		t.Skip("set MIYA_REMOTE_E2E=1 to test through the public relays")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	peers := make(chan int, 8)
	serve := func(ctx context.Context, conn *Conn) {
		if conn.Send(Frame{Type: "hello", Version: ProtocolVersion, Host: "test"}) != nil {
			return
		}
		var r Request
		if conn.Receive(&r) != nil {
			return
		}
		conn.Send(Frame{Type: "thread", Thread: r.Thread, Data: []byte(`{"Title":"echo"}`)})
		<-ctx.Done()
	}
	b, err := Start(ctx, Options{StateDir: dir, Serve: serve, OnPeers: func(n int) { peers <- n }})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	link := b.Link()
	if _, err := ParseLink(link); err != nil {
		t.Fatalf("ParseLink(Link()): %v", err)
	}
	SetClientSecret([]byte(strings.Repeat("s", 32)))
	defer SetClientSecret(nil)
	conn, err := Connect(ctx, link)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	var hello Frame
	if err := conn.Receive(&hello); err != nil || hello.Type != "hello" || hello.Version != ProtocolVersion {
		t.Fatalf("hello = %+v, %v", hello, err)
	}
	if err := conn.Send(Request{Op: "subscribe", Thread: 7}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var reply Frame
	if err := conn.Receive(&reply); err != nil || reply.Type != "thread" || reply.Thread != 7 || !strings.Contains(string(reply.Data), "echo") {
		t.Fatalf("reply = %+v, %v", reply, err)
	}
	select {
	case n := <-peers:
		if n != 1 {
			t.Errorf("peers = %d, want 1", n)
		}
	case <-time.After(5 * time.Second):
		t.Error("OnPeers not called")
	}
	conn.Close()
	b.Close()

	// The saved identity keeps the QR code across restarts.
	b2, err := Start(ctx, Options{StateDir: dir})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer b2.Close()
	if b2.Link() != link {
		t.Errorf("link changed across restart")
	}
}

// Verify HTTP and chat share a single Tailcat client without coupling their
// framing. Only a generated test string passes through the public relay.
func TestFilesHTTPOverExistingTunnel(t *testing.T) {
	if os.Getenv("MIYA_REMOTE_E2E") != "1" {
		t.Skip("set MIYA_REMOTE_E2E=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	content := "<!doctype html><h1>Tailcat file preview</h1>"
	bridge, err := Start(ctx, Options{StateDir: t.TempDir(), Files: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, "index.html", time.Time{}, strings.NewReader(content))
	}), Serve: func(ctx context.Context, c *Conn) {
		if c.Send(Frame{Type: "hello", Version: ProtocolVersion}) != nil {
			return
		}
		for {
			var request Request
			if c.Receive(&request) != nil {
				return
			}
			if c.Send(Frame{Type: "thread", Thread: request.Thread}) != nil {
				return
			}
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	conn, err := Connect(ctx, bridge.Link())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var hello Frame
	if err = conn.Receive(&hello); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) { return conn.DialFiles(ctx) }}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://desktop/index.html", nil)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(data) != content {
		t.Fatalf("file response %q: %v", data, err)
	}
	if err = conn.Send(Request{Op: "subscribe", Thread: 42}); err != nil {
		t.Fatal(err)
	}
	var reply Frame
	if err = conn.Receive(&reply); err != nil || reply.Thread != 42 {
		t.Fatalf("HTTP disrupted chat: %+v %v", reply, err)
	}
}
