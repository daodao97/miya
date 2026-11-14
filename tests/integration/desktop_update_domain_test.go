package integration

import (
	"encoding/json"
	"miya/internal/selfupdate"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
	"net"
	"sync"
	"testing"
)

func updateTestClient(t *testing.T, hello svcproto.Hello) (*svcclient.Client, func() []string) {
	t.Helper()
	conn, peer := net.Pipe()
	cl := svcclient.NewClient(conn)
	t.Cleanup(func() { cl.Close(); peer.Close() })
	var mu sync.Mutex
	var actions []string
	go func() {
		defer peer.Close()
		dec, enc := json.NewDecoder(peer), json.NewEncoder(peer)
		status := selfupdate.Status{Supported: true, Current: hello.Release, Latest: "1.2.3", Phase: "idle"}
		for {
			var req svcproto.Request
			if dec.Decode(&req) != nil {
				return
			}
			var result any
			switch req.Op {
			case "hello":
				result = hello
			case "miyaUpdate":
				var args struct{ Action, Version string }
				if json.Unmarshal(req.Args, &args) != nil {
					return
				}
				mu.Lock()
				actions = append(actions, args.Action+":"+args.Version)
				mu.Unlock()
				if args.Action == "install" {
					status.Phase = "waiting"
				}
				if args.Action == "check" {
					status.Phase = "available"
				}
				result = status
			default:
				t.Errorf("unexpected update operation %s", req.Op)
				return
			}
			if enc.Encode(map[string]any{"id": req.ID, "result": result}) != nil {
				return
			}
		}
	}()
	return cl, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), actions...) }
}
