//go:build !windows

package integration

import (
	"encoding/json"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
	"net"
	"sync"
	"testing"
)

// Only the test pipe is closed: these tests never restart the real service.
func restartTestClient(t *testing.T, hello svcproto.Hello, helloError string) (*svcclient.Client, func() []string) {
	t.Helper()
	conn, peer := net.Pipe()
	cl := svcclient.NewClient(conn)
	t.Cleanup(func() { cl.Close(); peer.Close() })
	var mu sync.Mutex
	var methods []string
	go func() {
		defer peer.Close()
		decoder, encoder := json.NewDecoder(peer), json.NewEncoder(peer)
		for {
			var req svcproto.Request
			if decoder.Decode(&req) != nil {
				return
			}
			mu.Lock()
			methods = append(methods, req.Op)
			mu.Unlock()
			reply := map[string]any{"id": req.ID}
			switch req.Op {
			case "hello":
				if helloError != "" {
					reply["error"] = helloError
				} else {
					reply["result"] = hello
				}
			case "quit":
				return // shutdown can close the connection before replying
			case "quitWhenIdle":
				if !hello.Busy {
					return
				}
			}
			if encoder.Encode(reply) != nil {
				return
			}
		}
	}()
	return cl, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), methods...)
	}
}
