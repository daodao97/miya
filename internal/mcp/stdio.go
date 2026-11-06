package mcp

import (
	"errors"
	"os"
	"sync"
	"time"

	"miya/internal/svcproto"
)

// Run serves MCP on stdio for the workspace named after -mcp, reaching
// the background service that is already running; it never starts one.
func Run() error {
	workspace, _ := os.Getwd()
	if len(os.Args) > 2 {
		workspace = os.Args[2]
	}
	// The service names its socket: agents may not pass MIYA_DIR on.
	sock := svcproto.Socket()
	if len(os.Args) > 3 {
		sock = os.Args[3]
	}
	var mu sync.Mutex
	var cl *svcproto.Client
	call := func(op string, args, out any) error {
		mu.Lock()
		if cl == nil {
			c, err := svcproto.Dial(sock)
			if err != nil {
				mu.Unlock()
				return errors.New("Miya background service is not running. Open Miya first.")
			}
			cl = c
		}
		c := cl
		mu.Unlock()
		err := c.Call(op, args, out)
		if errors.Is(err, svcproto.ErrClosed) {
			mu.Lock()
			cl = nil
			mu.Unlock()
		}
		return err
	}
	return Serve(os.Stdin, os.Stdout, workspace, call, time.Sleep)
}
