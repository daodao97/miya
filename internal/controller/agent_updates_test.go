package controller

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"miya/internal/agent/catalog"
	"miya/internal/agent/cliupdate"
)

func updateTestController(t *testing.T) (*Controller, chan func(), string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
	bin := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 1.2.3\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_BINARY", bin)
	q := make(chan func(), 10)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c := &Controller{Run: func(f func()) { q <- f }, ClaudeBinary: bin}
	c.Ctx = ctx
	c.Pi.Binary = bin
	return c, q, bin
}
func finishAgentCheck(t *testing.T, q chan func()) {
	t.Helper()
	select {
	case f := <-q:
		f()
	case <-time.After(3 * time.Second):
		t.Fatal("agent check did not finish")
	}
}

func TestAgentUpdateDiscoveryIsAsyncAndDeduplicated(t *testing.T) {
	c, q, _ := updateTestController(t)
	c.CheckAgentUpdates()
	for _, id := range catalog.IDs() {
		if !c.agentUpdates[id].Checking {
			t.Fatalf("%s not pending", id)
		}
	}
	c.CheckAgentUpdates()
	for range catalog.IDs() {
		finishAgentCheck(t, q)
	}
	for _, info := range c.AgentUpdates() {
		if info.Checking || info.Version != "1.2.3" || info.Update || info.Via != "" || info.Error != "" {
			t.Fatalf("unexpected state: %+v", info)
		}
	}
	if len(q) != 0 {
		t.Fatal("duplicate discoveries were scheduled")
	}
	// A snapshot must remain stable when another frame changes state.
	copy := c.AgentUpdates()
	delete(copy, catalog.Claude)
	if len(c.agentUpdates) != 3 {
		t.Fatal("caller mutated service state")
	}
}

func TestAgentUpgradeSerializesAndKeepsServiceAlive(t *testing.T) {
	c, q, bin := updateTestController(t)
	c.agentUpdates = map[string]cliupdate.Info{
		catalog.Claude: {Path: bin, Version: "1.2.3", Latest: "1.3.0", Update: true},
		catalog.Pi:     {Path: bin, Version: "1.2.3", Latest: "1.3.0", Update: true},
	}
	c.UpgradeAgent(catalog.Claude)
	if !c.AgentUpdateBusy() || !c.ServiceBusy() {
		t.Fatal("service can exit during an upgrade")
	}
	c.UpgradeAgent(catalog.Claude)
	c.UpgradeAgent(catalog.Pi)
	if c.agentUpdates[catalog.Pi].Updating {
		t.Fatal("two package managers may run together")
	}
	finishAgentCheck(t, q)
	if c.AgentUpdateBusy() || c.agentUpdates[catalog.Claude].Error == "" {
		t.Fatal("unsupported installation reported success or stayed busy")
	}
	if len(q) != 0 {
		t.Fatal("duplicate upgrade scheduled")
	}
}

// A check whose result waits for the main thread while the controller
// closes must not write the closed controller's state.
func TestAgentUpdateResultAfterCloseIsDropped(t *testing.T) {
	c, q, _ := updateTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	c.Ctx = ctx
	c.CheckAgentCLI = func(context.Context, string, string, []string) cliupdate.Info {
		return cliupdate.Info{Version: "9.9.9"}
	}
	c.CheckAgentUpdates()
	var results []func()
	for range catalog.IDs() {
		select {
		case f := <-q:
			results = append(results, f)
		case <-time.After(3 * time.Second):
			t.Fatal("agent check did not finish")
		}
	}
	cancel()
	for _, f := range results {
		f()
	}
	for id, info := range c.AgentUpdates() {
		if info.Version == "9.9.9" {
			t.Fatalf("%s: a result after close was written: %+v", id, info)
		}
	}
}

// A check still running as the controller closes dispatches nothing.
func TestAgentUpdateCheckRunningAtCloseDispatchesNothing(t *testing.T) {
	c, q, _ := updateTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	c.Ctx = ctx
	started, returned := make(chan struct{}, 10), make(chan struct{}, 10)
	c.CheckAgentCLI = func(ctx context.Context, _, _ string, _ []string) cliupdate.Info {
		started <- struct{}{}
		<-ctx.Done()
		defer func() { returned <- struct{}{} }()
		return cliupdate.Info{Version: "9.9.9"}
	}
	c.CheckAgentUpdates()
	for range catalog.IDs() {
		<-started
	}
	cancel()
	for range catalog.IDs() {
		<-returned
	}
	// Each goroutine reads parent.Err() right after its check returns; give
	// them the chance to (wrongly) dispatch before looking.
	time.Sleep(50 * time.Millisecond)
	if len(q) != 0 {
		t.Fatal("a check canceled by close was dispatched")
	}
}
