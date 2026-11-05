package main

import (
	"errors"
	"os"
	"sync"
	"time"
)

// miya -mcp <workspace> [socket] serves the workspace's conversations to the agents
// Miya runs, as an MCP server on stdio. Each agent starts its own; every tool
// call is a request to the background service, which holds the
// conversations, so all agents see and hand off work through one place.

// runMCP serves MCP on stdio for the workspace named after -mcp, reaching
// the background service that is already running; it never starts one.
func runMCP() error {
	workspace, _ := os.Getwd()
	if len(os.Args) > 2 {
		workspace = os.Args[2]
	}
	// The service names its socket: agents may not pass MIYA_DIR on.
	sock := serviceSocket()
	if len(os.Args) > 3 {
		sock = os.Args[3]
	}
	var mu sync.Mutex
	var cl *serviceClient
	call := func(op string, args, out any) error {
		mu.Lock()
		if cl == nil {
			c, err := dialService(sock)
			if err != nil {
				mu.Unlock()
				return errors.New("Miya background service is not running. Open Miya first.")
			}
			cl = c
		}
		c := cl
		mu.Unlock()
		err := c.Call(op, args, out)
		if errors.Is(err, errServiceClosed) {
			mu.Lock()
			cl = nil
			mu.Unlock()
		}
		return err
	}
	return serveMCP(os.Stdin, os.Stdout, workspace, call, time.Sleep)
}
